package db

import (
	"context"
	"fmt"
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
