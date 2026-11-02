package pipeline

import (
	"context"
	"errors"

	"github.com/rs/zerolog"
	"github.com/spelech/gmessage-rag/internal/db"
	"github.com/spelech/gmessage-rag/internal/gm"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
)

// MediaIngestor decrypts RCS attachments off fetched messages, persists the
// plaintext content-addressed to disk, and records media_attachments rows so the
// Vision service can pick them up (SEC-01).
//
// It delegates decryption, hashing, and duplicate avoidance to the existing
// gm.ExtractAndSaveMediaWithOptions path; it never re-implements that logic.
type MediaIngestor struct {
	Downloader        gm.MediaDownloader
	Database          *db.DB
	MediaDir          string
	PullFullSizeMedia bool
	Logger            zerolog.Logger
}

// IngestMessages persists media for a batch of raw messages and returns the
// number of attachment records written. A per-message download/save failure is
// non-blocking: it is logged and the remaining messages continue.
func (m *MediaIngestor) IngestMessages(ctx context.Context, messages []*gmproto.Message) (int, error) {
	if m == nil {
		return 0, errors.New("media ingestor cannot be nil")
	}
	if m.Downloader == nil {
		return 0, errors.New("media downloader cannot be nil")
	}
	if m.Database == nil {
		return 0, errors.New("database cannot be nil")
	}

	persisted := 0
	for _, msg := range messages {
		select {
		case <-ctx.Done():
			return persisted, ctx.Err()
		default:
		}

		if msg == nil {
			continue
		}

		attachments, err := gm.ExtractAndSaveMediaWithOptions(ctx, m.Downloader, msg, gm.MediaOptions{
			BaseDir:           m.MediaDir,
			PullFullSizeMedia: m.PullFullSizeMedia,
		})
		if err != nil {
			m.Logger.Warn().Err(err).Str("message_id", msg.GetMessageID()).Msg("Media extraction failed; continuing ingestion")
			continue
		}

		for _, att := range attachments {
			if err := m.Database.UpsertMediaAttachment(ctx, att); err != nil {
				m.Logger.Warn().Err(err).Str("message_id", att.MessageID).Msg("Persisting media attachment failed; continuing ingestion")
				continue
			}
			persisted++
		}
	}

	return persisted, nil
}

// IngestMessage persists media for a single raw message.
func (m *MediaIngestor) IngestMessage(ctx context.Context, msg *gmproto.Message) (int, error) {
	return m.IngestMessages(ctx, []*gmproto.Message{msg})
}
