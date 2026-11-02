package db

import (
	"context"
	"fmt"
	"time"
)

// BugleStatusDeliveredInferred indicates that an outgoing message was inferred as delivered/read
// based on a subsequent incoming reply timestamp in SMS or non-receipt RCS conversations.
const BugleStatusDeliveredInferred = 108

// InferDeliveryReceipts infers delivery receipts for all outgoing messages sent on or before replySentAt
// that have not yet received an explicit carrier/client delivery or read status (raw_status < 100 or NULL).
// It updates matching messages to BugleStatusDeliveredInferred and returns the count of updated records.
func (d *DB) InferDeliveryReceipts(ctx context.Context, conversationID string, replySentAt time.Time) (int64, error) {
	const updateInferredReceiptsSQL = `
UPDATE messages
SET raw_status = $1
WHERE conversation_id = $2
  AND is_from_me = TRUE
  AND sent_at <= $3
  AND (raw_status IS NULL OR raw_status < 100);
`

	tag, err := d.Pool.Exec(ctx, updateInferredReceiptsSQL, BugleStatusDeliveredInferred, conversationID, replySentAt.UTC())
	if err != nil {
		return 0, fmt.Errorf("infer delivery receipts for conv %s: %w", conversationID, err)
	}

	return tag.RowsAffected(), nil
}

// GetPendingUnacknowledgedMessages retrieves outgoing messages where is_from_me = true
// and (raw_status IS NULL or raw_status < 100), sorted by sent_at ASC, id ASC.
func (d *DB) GetPendingUnacknowledgedMessages(ctx context.Context, conversationID string) ([]Message, error) {
	const getPendingSQL = `
SELECT
    id, conversation_id, session_id, sender_id, sender_name,
    is_from_me, sent_at, body, reply_to_message_id, reactions,
    raw_status, is_backfill, created_at
FROM messages
WHERE conversation_id = $1
  AND is_from_me = TRUE
  AND (raw_status IS NULL OR raw_status < 100)
ORDER BY sent_at ASC, id ASC;
`

	rows, err := d.Pool.Query(ctx, getPendingSQL, conversationID)
	if err != nil {
		return nil, fmt.Errorf("query pending unacknowledged messages for conv %s: %w", conversationID, err)
	}
	defer rows.Close()

	return scanMessages(rows)
}
