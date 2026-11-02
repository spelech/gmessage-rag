package db

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// DossierChange captures a tracked-contact dossier transition used to build the briefing API (TRANS-03).
// It pairs the immediately preceding audit snapshot (contact_dossier_history) with the current
// contact_dossiers state so callers can diff "what changed" over a caller-supplied window.
type DossierChange struct {
	ConversationID   string
	ContactName      string
	CurrentVersion   int
	PreviousVersion  *int
	ChangedAt        time.Time
	PreviousMarkdown string
	CurrentMarkdown  string
	PreviousState    map[string]any
	CurrentState     map[string]any
}

// ListDossierChangesSince returns one change record per tracked contact whose dossier was created or
// updated strictly after since. Each record diff's the earliest contact_dossier_history snapshot
// written after since (the state when the window opened) against the current contact_dossiers state,
// so multi-update windows report the cumulative change. The previous snapshot is empty when the
// dossier has only its initial version.
//
// Requirement ID: TRANS-03
func (d *DB) ListDossierChangesSince(ctx context.Context, since time.Time) ([]DossierChange, error) {
	if d == nil || d.Pool == nil {
		return nil, fmt.Errorf("database pool is not initialized")
	}

	const changesSQL = `
SELECT
    d.conversation_id,
    d.contact_name,
    d.version,
    d.updated_at,
    d.markdown_content,
    d.structured_state,
    h.version,
    h.markdown_content,
    h.structured_state
FROM contact_dossiers d
JOIN conversations c ON c.id = d.conversation_id
LEFT JOIN LATERAL (
    SELECT
        h.version,
        h.markdown_content,
        h.structured_state,
        h.created_at
    FROM contact_dossier_history h
    WHERE h.conversation_id = d.conversation_id
      AND h.created_at > $1
    ORDER BY h.version ASC
    LIMIT 1
) h ON TRUE
WHERE c.is_tracked = TRUE
  AND (d.updated_at > $1 OR h.version IS NOT NULL)
ORDER BY d.updated_at DESC;
`

	sinceUTC := since.UTC()
	rows, err := d.Pool.Query(ctx, changesSQL, sinceUTC)
	if err != nil {
		return nil, fmt.Errorf("query dossier changes: %w", err)
	}
	defer rows.Close()

	var changes []DossierChange
	for rows.Next() {
		var (
			change       DossierChange
			prevVersion  *int
			prevMarkdown *string
			prevStateRaw []byte
			currentRaw   []byte
		)

		if err := rows.Scan(
			&change.ConversationID,
			&change.ContactName,
			&change.CurrentVersion,
			&change.ChangedAt,
			&change.CurrentMarkdown,
			&currentRaw,
			&prevVersion,
			&prevMarkdown,
			&prevStateRaw,
		); err != nil {
			return nil, fmt.Errorf("scan dossier change row: %w", err)
		}

		currentState, err := decodeState(currentRaw)
		if err != nil {
			return nil, fmt.Errorf("unmarshal current structured_state: %w", err)
		}
		change.CurrentState = currentState

		if prevVersion != nil {
			change.PreviousVersion = prevVersion
		}
		if prevMarkdown != nil {
			change.PreviousMarkdown = *prevMarkdown
		}
		prevState, err := decodeState(prevStateRaw)
		if err != nil {
			return nil, fmt.Errorf("unmarshal previous structured_state: %w", err)
		}
		change.PreviousState = prevState

		changes = append(changes, change)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate dossier change rows: %w", err)
	}

	return changes, nil
}

// decodeState unmarshals a JSONB structured_state payload, returning an empty map for NULL/empty.
func decodeState(raw []byte) (map[string]any, error) {
	if len(raw) == 0 {
		return make(map[string]any), nil
	}
	var state map[string]any
	if err := json.Unmarshal(raw, &state); err != nil {
		return nil, err
	}
	if state == nil {
		state = make(map[string]any)
	}
	return state, nil
}
