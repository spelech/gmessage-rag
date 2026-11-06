package db

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// MessageLink represents a causal or antecedent link between sessions (and optionally messages).
type MessageLink struct {
	ID              int64          `json:"id"`
	ConversationID  string         `json:"conversation_id"`
	SourceSessionID int64          `json:"source_session_id"`
	TargetSessionID int64          `json:"target_session_id"`
	SourceMessageID *string        `json:"source_message_id,omitempty"`
	TargetMessageID *string        `json:"target_message_id,omitempty"`
	LinkType        string         `json:"link_type"`
	Confidence      float64        `json:"confidence"`
	Evidence        map[string]any `json:"evidence"`
	Status          string         `json:"status"`
	CreatedBy       string         `json:"created_by"`
	CreatedAt       time.Time      `json:"created_at"`
}

// InsertMessageLink records a new directed link from sourceSessionID to targetSessionID.
func (d *DB) InsertMessageLink(ctx context.Context, link *MessageLink) (*MessageLink, error) {
	if d == nil || d.Pool == nil {
		return nil, fmt.Errorf("database not initialized")
	}
	if link == nil {
		return nil, fmt.Errorf("link cannot be nil")
	}
	if link.ConversationID == "" {
		return nil, fmt.Errorf("conversation_id cannot be empty")
	}
	if link.SourceSessionID == 0 || link.TargetSessionID == 0 {
		return nil, fmt.Errorf("source_session_id and target_session_id must be non-zero")
	}
	if link.SourceSessionID == link.TargetSessionID {
		return nil, fmt.Errorf("self-session links are not permitted")
	}

	evidenceJSON, err := json.Marshal(link.Evidence)
	if err != nil {
		evidenceJSON = []byte("{}")
	}

	status := link.Status
	if status == "" {
		status = "detected"
	}
	createdBy := link.CreatedBy
	if createdBy == "" {
		createdBy = "system"
	}
	confidence := link.Confidence
	if confidence <= 0 {
		confidence = 1.0
	}

	const query = `
INSERT INTO message_links (
    conversation_id,
    source_session_id,
    target_session_id,
    source_message_id,
    target_message_id,
    link_type,
    confidence,
    evidence,
    status,
    created_by
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING id, created_at;`

	var id int64
	var createdAt time.Time
	err = d.Pool.QueryRow(ctx, query,
		link.ConversationID,
		link.SourceSessionID,
		link.TargetSessionID,
		link.SourceMessageID,
		link.TargetMessageID,
		link.LinkType,
		confidence,
		evidenceJSON,
		status,
		createdBy,
	).Scan(&id, &createdAt)
	if err != nil {
		return nil, fmt.Errorf("insert message_link: %w", err)
	}

	link.ID = id
	link.Status = status
	link.CreatedBy = createdBy
	link.Confidence = confidence
	link.CreatedAt = createdAt
	return link, nil
}

// GetLinksForSession returns all links where the specified session is the source (i.e. references older sessions).
func (d *DB) GetLinksForSession(ctx context.Context, sessionID int64) ([]MessageLink, error) {
	if d == nil || d.Pool == nil {
		return nil, fmt.Errorf("database not initialized")
	}

	const query = `
SELECT id, conversation_id, source_session_id, target_session_id,
       source_message_id, target_message_id, link_type, confidence,
       evidence, status, created_by, created_at
FROM message_links
WHERE source_session_id = $1
ORDER BY created_at ASC;`

	rows, err := d.Pool.Query(ctx, query, sessionID)
	if err != nil {
		return nil, fmt.Errorf("query session links: %w", err)
	}
	defer rows.Close()

	var links []MessageLink
	for rows.Next() {
		var l MessageLink
		var evidenceBytes []byte
		if err := rows.Scan(
			&l.ID,
			&l.ConversationID,
			&l.SourceSessionID,
			&l.TargetSessionID,
			&l.SourceMessageID,
			&l.TargetMessageID,
			&l.LinkType,
			&l.Confidence,
			&evidenceBytes,
			&l.Status,
			&l.CreatedBy,
			&l.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan message link: %w", err)
		}
		if len(evidenceBytes) > 0 {
			_ = json.Unmarshal(evidenceBytes, &l.Evidence)
		}
		links = append(links, l)
	}
	return links, nil
}

// GetAntecedentSessions retrieves the full session records for all sessions linked as antecedents of sourceSessionIDs.
func (d *DB) GetAntecedentSessions(ctx context.Context, sourceSessionIDs []int64) ([]ConversationSession, error) {
	if d == nil || d.Pool == nil {
		return nil, fmt.Errorf("database not initialized")
	}
	if len(sourceSessionIDs) == 0 {
		return nil, nil
	}

	const query = `
SELECT DISTINCT s.id, s.conversation_id, s.started_at, s.ended_at,
       s.message_count, s.summary, s.transcript, s.is_processed, s.created_at, s.updated_at
FROM conversation_sessions s
JOIN message_links l ON l.target_session_id = s.id
WHERE l.source_session_id = ANY($1)
  AND l.status != 'rejected'
ORDER BY s.started_at ASC;`

	rows, err := d.Pool.Query(ctx, query, sourceSessionIDs)
	if err != nil {
		return nil, fmt.Errorf("query antecedent sessions: %w", err)
	}
	defer rows.Close()

	var sessions []ConversationSession
	for rows.Next() {
		var s ConversationSession
		if err := rows.Scan(
			&s.ID,
			&s.ConversationID,
			&s.StartedAt,
			&s.EndedAt,
			&s.MessageCount,
			&s.Summary,
			&s.Transcript,
			&s.IsProcessed,
			&s.CreatedAt,
			&s.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan antecedent session: %w", err)
		}
		sessions = append(sessions, s)
	}
	return sessions, nil
}

// PopulateDeterministicReplyLinks scans messages with reply_to_message_id and inserts causal links
// between their respective sessions if both messages have been assigned to sessions.
func (d *DB) PopulateDeterministicReplyLinks(ctx context.Context, conversationID string) (int64, error) {
	if d == nil || d.Pool == nil {
		return 0, fmt.Errorf("database not initialized")
	}

	const query = `
INSERT INTO message_links (
    conversation_id,
    source_session_id,
    target_session_id,
    source_message_id,
    target_message_id,
    link_type,
    confidence,
    evidence,
    status,
    created_by
)
SELECT
    m1.conversation_id,
    m1.session_id AS source_session_id,
    m2.session_id AS target_session_id,
    m1.id AS source_message_id,
    m2.id AS target_message_id,
    'explicit_reply' AS link_type,
    1.000 AS confidence,
    jsonb_build_object('source_sent_at', m1.sent_at, 'target_sent_at', m2.sent_at) AS evidence,
    'detected' AS status,
    'detector:protocol_reply' AS created_by
FROM messages m1
JOIN messages m2 ON m1.reply_to_message_id = m2.id
WHERE m1.conversation_id = $1
  AND m1.session_id IS NOT NULL
  AND m2.session_id IS NOT NULL
  AND m1.session_id <> m2.session_id
  AND NOT EXISTS (
      SELECT 1 FROM message_links ml
      WHERE ml.source_message_id = m1.id
        AND ml.target_message_id = m2.id
  );`

	tag, err := d.Pool.Exec(ctx, query, conversationID)
	if err != nil {
		return 0, fmt.Errorf("populate reply links: %w", err)
	}
	return tag.RowsAffected(), nil
}
