package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// UpsertConversation inserts or updates a conversation record with ON CONFLICT (id) DO UPDATE.
func (d *DB) UpsertConversation(ctx context.Context, conv *Conversation) error {
	if conv == nil {
		return fmt.Errorf("conversation cannot be nil")
	}

	participantsJSON, err := json.Marshal(conv.Participants)
	if err != nil {
		return fmt.Errorf("marshal participants: %w", err)
	}
	if conv.Participants == nil {
		participantsJSON = []byte("[]")
	}

	metadataJSON, err := json.Marshal(conv.Metadata)
	if err != nil {
		return fmt.Errorf("marshal metadata: %w", err)
	}
	if conv.Metadata == nil {
		metadataJSON = []byte("{}")
	}

	var lastMessageAt *time.Time
	if conv.LastMessageAt != nil {
		utc := conv.LastMessageAt.UTC()
		lastMessageAt = &utc
	}

	const upsertConvSQL = `
INSERT INTO conversations (
    id, name, participants, is_group, is_tracked, last_message_at,
    backfill_cursor, backfill_complete, metadata, created_at, updated_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NOW(), NOW())
ON CONFLICT (id) DO UPDATE SET
    name = EXCLUDED.name,
    participants = EXCLUDED.participants,
    is_group = EXCLUDED.is_group,
    is_tracked = EXCLUDED.is_tracked,
    last_message_at = COALESCE(EXCLUDED.last_message_at, conversations.last_message_at),
    backfill_cursor = COALESCE(EXCLUDED.backfill_cursor, conversations.backfill_cursor),
    backfill_complete = EXCLUDED.backfill_complete,
    metadata = EXCLUDED.metadata,
    updated_at = NOW()
RETURNING created_at, updated_at;
`

	err = d.Pool.QueryRow(ctx, upsertConvSQL,
		conv.ID,
		conv.Name,
		participantsJSON,
		conv.IsGroup,
		conv.IsTracked,
		lastMessageAt,
		conv.BackfillCursor,
		conv.BackfillComplete,
		metadataJSON,
	).Scan(&conv.CreatedAt, &conv.UpdatedAt)
	if err != nil {
		return fmt.Errorf("upsert conversation: %w", err)
	}

	return nil
}

// GetConversation retrieves a conversation by ID.
func (d *DB) GetConversation(ctx context.Context, id string) (*Conversation, error) {
	const getConvSQL = `
SELECT
    id, name, participants, is_group, is_tracked, last_message_at,
    backfill_cursor, backfill_complete, metadata, created_at, updated_at
FROM conversations
WHERE id = $1;
`

	var conv Conversation
	var participantsRaw []byte
	var metadataRaw []byte

	err := d.Pool.QueryRow(ctx, getConvSQL, id).Scan(
		&conv.ID,
		&conv.Name,
		&participantsRaw,
		&conv.IsGroup,
		&conv.IsTracked,
		&conv.LastMessageAt,
		&conv.BackfillCursor,
		&conv.BackfillComplete,
		&metadataRaw,
		&conv.CreatedAt,
		&conv.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get conversation: %w", err)
	}

	if len(participantsRaw) > 0 {
		if err := json.Unmarshal(participantsRaw, &conv.Participants); err != nil {
			return nil, fmt.Errorf("unmarshal participants: %w", err)
		}
	}
	if conv.Participants == nil {
		conv.Participants = []string{}
	}

	if len(metadataRaw) > 0 {
		if err := json.Unmarshal(metadataRaw, &conv.Metadata); err != nil {
			return nil, fmt.Errorf("unmarshal metadata: %w", err)
		}
	}
	if conv.Metadata == nil {
		conv.Metadata = make(map[string]any)
	}

	return &conv, nil
}

// ListConversations lists conversations ordered by most recent message activity.
func (d *DB) ListConversations(ctx context.Context, trackedOnly bool, limit int) ([]Conversation, error) {
	if limit <= 0 {
		limit = 50
	}

	const listConvSQL = `
SELECT
    id, name, participants, is_group, is_tracked, last_message_at,
    backfill_cursor, backfill_complete, metadata, created_at, updated_at
FROM conversations
WHERE ($1 = FALSE OR is_tracked = TRUE)
ORDER BY last_message_at DESC NULLS LAST, created_at DESC
LIMIT $2;
`

	rows, err := d.Pool.Query(ctx, listConvSQL, trackedOnly, limit)
	if err != nil {
		return nil, fmt.Errorf("list conversations: %w", err)
	}
	defer rows.Close()

	var convs []Conversation
	for rows.Next() {
		var conv Conversation
		var participantsRaw []byte
		var metadataRaw []byte

		if err := rows.Scan(
			&conv.ID,
			&conv.Name,
			&participantsRaw,
			&conv.IsGroup,
			&conv.IsTracked,
			&conv.LastMessageAt,
			&conv.BackfillCursor,
			&conv.BackfillComplete,
			&metadataRaw,
			&conv.CreatedAt,
			&conv.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan conversation row: %w", err)
		}

		if len(participantsRaw) > 0 {
			if err := json.Unmarshal(participantsRaw, &conv.Participants); err != nil {
				return nil, fmt.Errorf("unmarshal participants: %w", err)
			}
		}
		if conv.Participants == nil {
			conv.Participants = []string{}
		}

		if len(metadataRaw) > 0 {
			if err := json.Unmarshal(metadataRaw, &conv.Metadata); err != nil {
				return nil, fmt.Errorf("unmarshal metadata: %w", err)
			}
		}
		if conv.Metadata == nil {
			conv.Metadata = make(map[string]any)
		}

		convs = append(convs, conv)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate conversation rows: %w", err)
	}

	return convs, nil
}

// SetConversationTracked updates the is_tracked flag for a conversation.
func (d *DB) SetConversationTracked(ctx context.Context, conversationID string, isTracked bool) error {
	const updateTrackedSQL = `
UPDATE conversations
SET is_tracked = $2,
    updated_at = NOW()
WHERE id = $1;
`

	tag, err := d.Pool.Exec(ctx, updateTrackedSQL, conversationID, isTracked)
	if err != nil {
		return fmt.Errorf("update conversation tracking status: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ResetConversationBackfill clears the backfill cursor and completion flag
// so a historical pagination pass can be re-initiated from the start.
func (d *DB) ResetConversationBackfill(ctx context.Context, conversationID string) error {
	if d == nil || d.Pool == nil {
		return errors.New("database pool is not initialized")
	}
	if conversationID == "" {
		return errors.New("conversationID cannot be empty")
	}

	const resetBackfillSQL = `
UPDATE conversations
SET backfill_cursor = NULL,
    backfill_complete = FALSE,
    updated_at = NOW()
WHERE id = $1;
`

	tag, err := d.Pool.Exec(ctx, resetBackfillSQL, conversationID)
	if err != nil {
		return fmt.Errorf("reset conversation backfill: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}

	return nil
}

// UpdateConversationBackfillCursor updates the backfill cursor for a conversation.
func (d *DB) UpdateConversationBackfillCursor(ctx context.Context, conversationID string, cursor *string) error {
	if d == nil || d.Pool == nil {
		return errors.New("database pool is not initialized")
	}
	if conversationID == "" {
		return errors.New("conversationID cannot be empty")
	}

	const updateCursorSQL = `
UPDATE conversations
SET backfill_cursor = $2,
    updated_at = NOW()
WHERE id = $1;
`

	tag, err := d.Pool.Exec(ctx, updateCursorSQL, conversationID, cursor)
	if err != nil {
		return fmt.Errorf("update conversation backfill_cursor: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}

	return nil
}
