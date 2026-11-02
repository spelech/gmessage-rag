package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// GetContactDossier retrieves the current living dossier for a contact.
// It resolves contactIdentifier via:
// 1. Direct conversation ID match (in contact_dossiers or conversations)
// 2. Phone number in conversation participants JSON array
// 3. Contact name match (contact_dossiers.contact_name or conversations.name)
func (d *DB) GetContactDossier(ctx context.Context, contactIdentifier string) (*ContactDossier, error) {
	if contactIdentifier == "" {
		return nil, fmt.Errorf("contact identifier cannot be empty")
	}

	phoneJSON, err := json.Marshal([]string{contactIdentifier})
	if err != nil {
		phoneJSON = []byte("[]")
	}

	const getDossierSQL = `
SELECT
    d.conversation_id,
    d.contact_name,
    d.markdown_content,
    d.structured_state,
    d.last_processed_message_at,
    d.version,
    d.updated_at
FROM contact_dossiers d
JOIN conversations c ON d.conversation_id = c.id
WHERE d.conversation_id = $1
   OR c.participants @> $2::jsonb
   OR d.contact_name ILIKE $1
   OR c.name ILIKE $1
ORDER BY d.updated_at DESC
LIMIT 1;
`

	var dossier ContactDossier
	var structuredStateRaw []byte

	err = d.Pool.QueryRow(ctx, getDossierSQL, contactIdentifier, string(phoneJSON)).Scan(
		&dossier.ConversationID,
		&dossier.ContactName,
		&dossier.MarkdownContent,
		&structuredStateRaw,
		&dossier.LastProcessedMessageAt,
		&dossier.Version,
		&dossier.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get contact dossier: %w", err)
	}

	if len(structuredStateRaw) > 0 {
		if err := json.Unmarshal(structuredStateRaw, &dossier.StructuredState); err != nil {
			return nil, fmt.Errorf("unmarshal structured_state: %w", err)
		}
	}
	if dossier.StructuredState == nil {
		dossier.StructuredState = make(map[string]any)
	}

	return &dossier, nil
}

// GetDossierLiveTail returns all raw messages for conversationID sent strictly after lastProcessedAt,
// ordered chronologically (sent_at ASC, id ASC).
// If lastProcessedAt is nil, all raw messages for the conversation are returned.
func (d *DB) GetDossierLiveTail(ctx context.Context, conversationID string, lastProcessedAt *time.Time) ([]Message, error) {
	if conversationID == "" {
		return nil, fmt.Errorf("conversation ID cannot be empty")
	}

	const liveTailSQL = `
SELECT
    id, conversation_id, session_id, sender_id, sender_name,
    is_from_me, sent_at, body, reply_to_message_id, reactions,
    raw_status, is_backfill, created_at
FROM messages
WHERE conversation_id = $1
  AND ($2::timestamptz IS NULL OR sent_at > $2)
ORDER BY sent_at ASC, id ASC;
`

	var lastProcessedUTC *time.Time
	if lastProcessedAt != nil {
		t := lastProcessedAt.UTC()
		lastProcessedUTC = &t
	}

	rows, err := d.Pool.Query(ctx, liveTailSQL, conversationID, lastProcessedUTC)
	if err != nil {
		return nil, fmt.Errorf("query live tail messages: %w", err)
	}
	defer rows.Close()

	return scanMessages(rows)
}

// UpsertContactDossier persists a living dossier for conversationID.
// If an existing dossier is found, its prior state is snapshotted into contact_dossier_history
// with the existing version and triggerSessionID.
// Then contact_dossiers is upserted incrementing version (version = existing.version + 1, or 1 if new),
// updating markdown_content, structured_state, last_processed_message_at, contact_name, and updated_at.
func (d *DB) UpsertContactDossier(
	ctx context.Context,
	conversationID string,
	contactName string,
	markdown string,
	structuredState map[string]any,
	triggerSessionID *int64,
	lastProcessedMessageAt *time.Time,
) (*ContactDossier, error) {
	if conversationID == "" {
		return nil, fmt.Errorf("conversation ID cannot be empty")
	}

	stateJSON, err := json.Marshal(structuredState)
	if err != nil {
		return nil, fmt.Errorf("marshal structured_state: %w", err)
	}
	if structuredState == nil {
		stateJSON = []byte("{}")
	}

	var lastProcessedUTC *time.Time
	if lastProcessedMessageAt != nil {
		t := lastProcessedMessageAt.UTC()
		lastProcessedUTC = &t
	}

	tx, err := d.Pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	// Check if existing dossier exists
	const checkExistingSQL = `
SELECT version, markdown_content, structured_state
FROM contact_dossiers
WHERE conversation_id = $1
FOR UPDATE;
`

	var existingVersion int
	var existingMarkdown string
	var existingStateRaw []byte

	err = tx.QueryRow(ctx, checkExistingSQL, conversationID).Scan(
		&existingVersion,
		&existingMarkdown,
		&existingStateRaw,
	)

	var nextVersion int
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// First time creating dossier
			nextVersion = 1
		} else {
			return nil, fmt.Errorf("check existing dossier: %w", err)
		}
	} else {
		// Existing record found: snapshot into history
		const snapshotHistorySQL = `
INSERT INTO contact_dossier_history (
    conversation_id, version, markdown_content, structured_state, trigger_session_id, created_at
) VALUES ($1, $2, $3, $4, $5, NOW());
`
		_, err = tx.Exec(ctx, snapshotHistorySQL,
			conversationID,
			existingVersion,
			existingMarkdown,
			existingStateRaw,
			triggerSessionID,
		)
		if err != nil {
			return nil, fmt.Errorf("snapshot dossier history: %w", err)
		}
		nextVersion = existingVersion + 1
	}

	// Upsert contact_dossiers
	const upsertDossierSQL = `
INSERT INTO contact_dossiers (
    conversation_id,
    contact_name,
    markdown_content,
    structured_state,
    last_processed_message_at,
    version,
    updated_at
) VALUES ($1, $2, $3, $4, $5, $6, NOW())
ON CONFLICT (conversation_id) DO UPDATE SET
    contact_name = EXCLUDED.contact_name,
    markdown_content = EXCLUDED.markdown_content,
    structured_state = EXCLUDED.structured_state,
    last_processed_message_at = EXCLUDED.last_processed_message_at,
    version = EXCLUDED.version,
    updated_at = NOW()
RETURNING updated_at;
`

	var updatedAt time.Time
	err = tx.QueryRow(ctx, upsertDossierSQL,
		conversationID,
		contactName,
		markdown,
		stateJSON,
		lastProcessedUTC,
		nextVersion,
	).Scan(&updatedAt)
	if err != nil {
		return nil, fmt.Errorf("upsert contact dossier: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit tx: %w", err)
	}

	finalState := structuredState
	if finalState == nil {
		finalState = make(map[string]any)
	}

	return &ContactDossier{
		ConversationID:         conversationID,
		ContactName:            contactName,
		MarkdownContent:        markdown,
		StructuredState:        finalState,
		LastProcessedMessageAt: lastProcessedUTC,
		Version:                nextVersion,
		UpdatedAt:              updatedAt,
	}, nil
}

// GetDossierHistory retrieves audit snapshots for conversationID ordered by version DESC.
func (d *DB) GetDossierHistory(ctx context.Context, conversationID string, limit int) ([]ContactDossierHistory, error) {
	if conversationID == "" {
		return nil, fmt.Errorf("conversation ID cannot be empty")
	}
	if limit <= 0 {
		limit = 20
	}

	const getHistorySQL = `
SELECT
    id,
    conversation_id,
    version,
    markdown_content,
    structured_state,
    trigger_session_id,
    created_at
FROM contact_dossier_history
WHERE conversation_id = $1
ORDER BY version DESC, created_at DESC
LIMIT $2;
`

	rows, err := d.Pool.Query(ctx, getHistorySQL, conversationID, limit)
	if err != nil {
		return nil, fmt.Errorf("query dossier history: %w", err)
	}
	defer rows.Close()

	var history []ContactDossierHistory
	for rows.Next() {
		var item ContactDossierHistory
		var stateRaw []byte

		if err := rows.Scan(
			&item.ID,
			&item.ConversationID,
			&item.Version,
			&item.MarkdownContent,
			&stateRaw,
			&item.TriggerSessionID,
			&item.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan dossier history row: %w", err)
		}

		if len(stateRaw) > 0 {
			if err := json.Unmarshal(stateRaw, &item.StructuredState); err != nil {
				return nil, fmt.Errorf("unmarshal history structured_state: %w", err)
			}
		}
		if item.StructuredState == nil {
			item.StructuredState = make(map[string]any)
		}

		history = append(history, item)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate dossier history rows: %w", err)
	}

	return history, nil
}
