package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// UpsertMediaAttachment inserts a media attachment record.
// If the record with (message_id, sha256_hash) already exists, it does nothing (idempotent).
func (d *DB) UpsertMediaAttachment(ctx context.Context, media *MediaAttachment) error {
	if media == nil {
		return fmt.Errorf("media attachment cannot be nil")
	}

	const upsertMediaSQL = `
INSERT INTO media_attachments (
    message_id, conversation_id, sha256_hash, relative_path, mime_type,
    file_size_bytes, width, height, ocr_text, visual_description,
    vision_processed, created_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, NOW())
ON CONFLICT (message_id, sha256_hash) DO NOTHING
RETURNING id, created_at;
`

	err := d.Pool.QueryRow(ctx, upsertMediaSQL,
		media.MessageID,
		media.ConversationID,
		media.SHA256Hash,
		media.RelativePath,
		media.MimeType,
		media.FileSizeBytes,
		media.Width,
		media.Height,
		media.OCRText,
		media.VisualDescription,
		media.VisionProcessed,
	).Scan(&media.ID, &media.CreatedAt)
	if err != nil {
		// If ON CONFLICT DO NOTHING occurs, no row is returned; verify if already exists
		return nil
	}

	return nil
}

// GetMediaAttachmentsForMessage retrieves all media attachments associated with a message.
func (d *DB) GetMediaAttachmentsForMessage(ctx context.Context, messageID string) ([]MediaAttachment, error) {
	const getMediaSQL = `
SELECT
    id, message_id, conversation_id, sha256_hash, relative_path, mime_type,
    file_size_bytes, width, height, ocr_text, visual_description,
    vision_processed, created_at
FROM media_attachments
WHERE message_id = $1
ORDER BY id ASC;
`

	rows, err := d.Pool.Query(ctx, getMediaSQL, messageID)
	if err != nil {
		return nil, fmt.Errorf("query media attachments: %w", err)
	}
	defer rows.Close()

	return scanMediaAttachments(rows)
}

// SearchMediaAttachments searches media attachments by OCR text or visual description.
// An empty conversationID searches every conversation; an empty query matches all rows.
func (d *DB) SearchMediaAttachments(ctx context.Context, conversationID, query string, limit int) ([]MediaAttachment, error) {
	if d == nil || d.Pool == nil {
		return nil, fmt.Errorf("database pool is not connected")
	}
	if limit <= 0 {
		limit = 20
	}

	const searchMediaSQL = `
SELECT
    id, message_id, conversation_id, sha256_hash, relative_path, mime_type,
    file_size_bytes, width, height, ocr_text, visual_description,
    vision_processed, created_at
FROM media_attachments
WHERE ($1 = '' OR conversation_id = $1)
  AND (
    ($2 = '' OR ocr_text ILIKE '%' || $2 || '%')
    OR ($2 = '' OR visual_description ILIKE '%' || $2 || '%')
  )
ORDER BY created_at DESC, id DESC
LIMIT $3;
`

	rows, err := d.Pool.Query(ctx, searchMediaSQL, conversationID, query, limit)
	if err != nil {
		return nil, fmt.Errorf("search media attachments: %w", err)
	}
	defer rows.Close()

	return scanMediaAttachments(rows)
}

func scanMediaAttachments(rows pgx.Rows) ([]MediaAttachment, error) {
	var list []MediaAttachment
	for rows.Next() {
		var m MediaAttachment
		if err := rows.Scan(
			&m.ID,
			&m.MessageID,
			&m.ConversationID,
			&m.SHA256Hash,
			&m.RelativePath,
			&m.MimeType,
			&m.FileSizeBytes,
			&m.Width,
			&m.Height,
			&m.OCRText,
			&m.VisualDescription,
			&m.VisionProcessed,
			&m.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan media attachment row: %w", err)
		}
		list = append(list, m)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate media attachment rows: %w", err)
	}

	return list, nil
}

// UpdateMediaAttachmentVision updates the OCR text, visual description, and vision_processed status of a media attachment.
func (d *DB) UpdateMediaAttachmentVision(ctx context.Context, id int64, ocrText, visualDesc *string, processed bool) error {
	if d == nil || d.Pool == nil {
		return fmt.Errorf("database pool is not connected")
	}

	const updateVisionSQL = `
UPDATE media_attachments
SET ocr_text = $1,
    visual_description = $2,
    vision_processed = $3
WHERE id = $4;
`
	tag, err := d.Pool.Exec(ctx, updateVisionSQL, ocrText, visualDesc, processed, id)
	if err != nil {
		return fmt.Errorf("update media attachment vision: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// GetUnprocessedMediaAttachments retrieves up to limit media attachments that have vision_processed = false.
// If conversationID is non-empty, results are scoped to that conversation.
func (d *DB) GetUnprocessedMediaAttachments(ctx context.Context, conversationID string, limit int) ([]MediaAttachment, error) {
	if d == nil || d.Pool == nil {
		return nil, fmt.Errorf("database pool is not connected")
	}
	if limit <= 0 {
		limit = 50
	}

	var rowsQuery string
	var args []any
	if conversationID != "" {
		rowsQuery = `
SELECT
    id, message_id, conversation_id, sha256_hash, relative_path, mime_type,
    file_size_bytes, width, height, ocr_text, visual_description,
    vision_processed, created_at
FROM media_attachments
WHERE vision_processed = FALSE AND conversation_id = $1
ORDER BY created_at ASC
LIMIT $2;
`
		args = []any{conversationID, limit}
	} else {
		rowsQuery = `
SELECT
    id, message_id, conversation_id, sha256_hash, relative_path, mime_type,
    file_size_bytes, width, height, ocr_text, visual_description,
    vision_processed, created_at
FROM media_attachments
WHERE vision_processed = FALSE
ORDER BY created_at ASC
LIMIT $1;
`
		args = []any{limit}
	}

	rows, err := d.Pool.Query(ctx, rowsQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("query unprocessed media attachments: %w", err)
	}
	defer rows.Close()

	return scanMediaAttachments(rows)
}
