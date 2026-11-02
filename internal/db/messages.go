package db

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// UpsertMessage inserts a single message idempotently with ON CONFLICT (id) DO NOTHING.
func (d *DB) UpsertMessage(ctx context.Context, msg *Message) error {
	if msg == nil {
		return fmt.Errorf("message cannot be nil")
	}

	n, err := d.UpsertMessagesBatch(ctx, []*Message{msg})
	if err != nil {
		return err
	}
	_ = n
	return nil
}

// UpsertMessagesBatch inserts multiple messages in a single transaction with ON CONFLICT (id) DO NOTHING.
// Returns the number of new messages actually inserted.
func (d *DB) UpsertMessagesBatch(ctx context.Context, msgs []*Message) (int64, error) {
	if len(msgs) == 0 {
		return 0, nil
	}

	tx, err := d.Pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	const insertMsgSQL = `
INSERT INTO messages (
    id, conversation_id, session_id, sender_id, sender_name,
    is_from_me, sent_at, body, reply_to_message_id, reactions,
    raw_status, is_backfill, created_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, NOW())
ON CONFLICT (id) DO NOTHING;
`

	batch := &pgx.Batch{}
	for _, msg := range msgs {
		reactionsJSON, err := json.Marshal(msg.Reactions)
		if err != nil {
			return 0, fmt.Errorf("marshal reactions for msg %s: %w", msg.ID, err)
		}
		if msg.Reactions == nil {
			reactionsJSON = []byte("[]")
		}

		sentAt := msg.SentAt.UTC()

		batch.Queue(insertMsgSQL,
			msg.ID,
			msg.ConversationID,
			msg.SessionID,
			msg.SenderID,
			msg.SenderName,
			msg.IsFromMe,
			sentAt,
			msg.Body,
			msg.ReplyToMessageID,
			reactionsJSON,
			msg.RawStatus,
			msg.IsBackfill,
		)
	}

	br := tx.SendBatch(ctx, batch)
	var insertedCount int64

	for range msgs {
		cmdTag, err := br.Exec()
		if err != nil {
			br.Close()
			return 0, fmt.Errorf("exec batch insert message: %w", err)
		}
		insertedCount += cmdTag.RowsAffected()
	}

	if err := br.Close(); err != nil {
		return 0, fmt.Errorf("close batch: %w", err)
	}

	// Update conversation last_message_at for conversations present in this batch
	convLatest := make(map[string]time.Time)
	for _, msg := range msgs {
		t := msg.SentAt.UTC()
		if cur, ok := convLatest[msg.ConversationID]; !ok || t.After(cur) {
			convLatest[msg.ConversationID] = t
		}
	}

	const updateConvLastMsgSQL = `
UPDATE conversations
SET last_message_at = GREATEST(last_message_at, $2),
    updated_at = NOW()
WHERE id = $1;
`
	for convID, latest := range convLatest {
		if _, err := tx.Exec(ctx, updateConvLastMsgSQL, convID, latest); err != nil {
			return 0, fmt.Errorf("update conversation last_message_at: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit tx: %w", err)
	}

	return insertedCount, nil
}

// UpdateReactions replaces the reactions on a message atomically with deduplication by (sender, emoji).
func (d *DB) UpdateReactions(ctx context.Context, messageID string, reactions []Reaction) error {
	type reactionKey struct {
		sender string
		emoji  string
	}
	seen := make(map[reactionKey]bool)
	deduped := make([]Reaction, 0, len(reactions))
	for _, r := range reactions {
		k := reactionKey{sender: r.Sender, emoji: r.Emoji}
		if !seen[k] {
			seen[k] = true
			deduped = append(deduped, r)
		}
	}

	reactionsJSON, err := json.Marshal(deduped)
	if err != nil {
		return fmt.Errorf("marshal reactions: %w", err)
	}
	if len(deduped) == 0 {
		reactionsJSON = []byte("[]")
	}

	const updateReactionsSQL = `
UPDATE messages
SET reactions = $2
WHERE id = $1;
`
	tag, err := d.Pool.Exec(ctx, updateReactionsSQL, messageID, reactionsJSON)
	if err != nil {
		return fmt.Errorf("update reactions: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}

	return nil
}

// GetMessagesAroundTime retrieves messages for a conversation within ±windowMinutes of targetTime, sorted by sent_at ASC.
func (d *DB) GetMessagesAroundTime(ctx context.Context, conversationID string, targetTime time.Time, windowMinutes int) ([]Message, error) {
	if windowMinutes <= 0 {
		windowMinutes = 30
	}
	targetUTC := targetTime.UTC()
	windowDuration := time.Duration(windowMinutes) * time.Minute
	timeStart := targetUTC.Add(-windowDuration)
	timeEnd := targetUTC.Add(windowDuration)

	const getMessagesAroundTimeSQL = `
SELECT
    id, conversation_id, session_id, sender_id, sender_name,
    is_from_me, sent_at, body, reply_to_message_id, reactions,
    raw_status, is_backfill, created_at
FROM messages
WHERE conversation_id = $1
  AND sent_at >= $2
  AND sent_at <= $3
ORDER BY sent_at ASC, id ASC;
`

	rows, err := d.Pool.Query(ctx, getMessagesAroundTimeSQL, conversationID, timeStart, timeEnd)
	if err != nil {
		return nil, fmt.Errorf("query messages around time: %w", err)
	}
	defer rows.Close()

	return scanMessages(rows)
}

// GetUnassignedMessages retrieves all messages for a conversation that have not yet been assigned to a session (session_id IS NULL), sorted by sent_at ASC.
func (d *DB) GetUnassignedMessages(ctx context.Context, conversationID string) ([]Message, error) {
	const getUnassignedSQL = `
SELECT
    id, conversation_id, session_id, sender_id, sender_name,
    is_from_me, sent_at, body, reply_to_message_id, reactions,
    raw_status, is_backfill, created_at
FROM messages
WHERE conversation_id = $1
  AND session_id IS NULL
ORDER BY sent_at ASC, id ASC;
`

	rows, err := d.Pool.Query(ctx, getUnassignedSQL, conversationID)
	if err != nil {
		return nil, fmt.Errorf("query unassigned messages: %w", err)
	}
	defer rows.Close()

	return scanMessages(rows)
}

func scanMessages(rows pgx.Rows) ([]Message, error) {
	var msgs []Message
	for rows.Next() {
		var m Message
		var reactionsRaw []byte

		if err := rows.Scan(
			&m.ID,
			&m.ConversationID,
			&m.SessionID,
			&m.SenderID,
			&m.SenderName,
			&m.IsFromMe,
			&m.SentAt,
			&m.Body,
			&m.ReplyToMessageID,
			&reactionsRaw,
			&m.RawStatus,
			&m.IsBackfill,
			&m.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan message row: %w", err)
		}

		if len(reactionsRaw) > 0 {
			if err := json.Unmarshal(reactionsRaw, &m.Reactions); err != nil {
				return nil, fmt.Errorf("unmarshal reactions: %w", err)
			}
		}
		if m.Reactions == nil {
			m.Reactions = []Reaction{}
		}

		msgs = append(msgs, m)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate message rows: %w", err)
	}

	return msgs, nil
}
