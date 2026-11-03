package pipeline

import (
	"context"
	"errors"
	"fmt"

	"github.com/spelech/gmessage-rag/internal/db"
	"github.com/spelech/gmessage-rag/internal/gm"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
)

// ETLPipeline standardizes historical message backfill and streaming ingestion into
// a three-stage ETL pipeline (PIPE-06).
type ETLPipeline struct {
	Fetcher  gm.MessageFetcher
	Database *db.DB
	// Media, when set, persists decrypted attachments for fetched messages so the
	// Vision service can OCR them (SEC-01).
	Media *MediaIngestor
}

// Extract fetches a page of messages using Fetcher.FetchMessages.
// Returns the raw protobuf messages, the next cursor bytes (if any), and any error encountered.
func (p *ETLPipeline) Extract(ctx context.Context, conversationID string, count int64, cursor []byte) ([]*gmproto.Message, []byte, error) {
	if p == nil || p.Fetcher == nil {
		return nil, nil, errors.New("fetcher cannot be nil")
	}
	if conversationID == "" {
		return nil, nil, errors.New("conversationID cannot be empty")
	}

	resp, err := p.Fetcher.FetchMessages(ctx, conversationID, count, cursor)
	if err != nil {
		return nil, nil, fmt.Errorf("fetch messages: %w", err)
	}
	if resp == nil {
		return nil, nil, nil
	}

	var nextCursor []byte
	if c := resp.GetCursor(); c != nil && c.GetLastItemID() != "" {
		if c.GetLastItemTimestamp() != 0 {
			nextCursor = []byte(fmt.Sprintf("%s:%d", c.GetLastItemID(), c.GetLastItemTimestamp()))
		} else {
			nextCursor = []byte(c.GetLastItemID())
		}
	}

	return resp.GetMessages(), nextCursor, nil
}

// Transform maps raw protobufs to []*db.Message, normalizing timestamps with
// gm.NormalizeTimestamp and resolving direction with gm.ResolveSenderDirection.
// The shared gm.ProtoMessageToDBMessage helper keeps streaming and backfill
// conversions identical (DRY).
func (p *ETLPipeline) Transform(rawMsgs []*gmproto.Message, conversationID string) []*db.Message {
	if len(rawMsgs) == 0 {
		return []*db.Message{}
	}

	dbMsgs := make([]*db.Message, 0, len(rawMsgs))
	for _, rawMsg := range rawMsgs {
		if dbMsg := gm.ProtoMessageToDBMessage(rawMsg, conversationID, true); dbMsg != nil {
			dbMsgs = append(dbMsgs, dbMsg)
		}
	}

	return dbMsgs
}

// Load batch-upserts messages with Database.UpsertMessagesBatch and updates conversation BackfillCursor.
func (p *ETLPipeline) Load(ctx context.Context, conversationID string, msgs []*db.Message, nextCursor []byte) error {
	if p == nil || p.Database == nil {
		return errors.New("database cannot be nil")
	}
	if conversationID == "" {
		return errors.New("conversationID cannot be empty")
	}

	if len(msgs) > 0 {
		if _, err := p.Database.UpsertMessagesBatch(ctx, msgs); err != nil {
			return fmt.Errorf("upsert messages batch: %w", err)
		}
	}

	var cursorPtr *string
	if len(nextCursor) > 0 {
		cursorStr := string(nextCursor)
		cursorPtr = &cursorStr
	}

	if err := p.Database.UpdateConversationBackfillCursor(ctx, conversationID, cursorPtr); err != nil {
		return fmt.Errorf("update conversation cursor: %w", err)
	}

	return nil
}

// RunBatch executes Extract -> Transform -> Load sequentially.
// Returns the next cursor bytes, the number of messages inserted in the batch, and any error encountered.
func (p *ETLPipeline) RunBatch(ctx context.Context, conversationID string, count int64, cursor []byte) ([]byte, int, error) {
	select {
	case <-ctx.Done():
		return nil, 0, ctx.Err()
	default:
	}

	rawMsgs, nextCursor, err := p.Extract(ctx, conversationID, count, cursor)
	if err != nil {
		return nil, 0, fmt.Errorf("extract stage: %w", err)
	}

	transformedMsgs := p.Transform(rawMsgs, conversationID)

	select {
	case <-ctx.Done():
		return nil, 0, ctx.Err()
	default:
	}

	if err := p.Load(ctx, conversationID, transformedMsgs, nextCursor); err != nil {
		return nil, 0, fmt.Errorf("load stage: %w", err)
	}

	// Persist decrypted media attachments for the freshly loaded messages so the
	// Vision service can OCR them on the next debounced flush (SEC-01).
	if p.Media != nil {
		if _, err := p.Media.IngestMessages(ctx, rawMsgs); err != nil {
			return nextCursor, len(transformedMsgs), fmt.Errorf("media ingestion stage: %w", err)
		}
	}

	return nextCursor, len(transformedMsgs), nil
}
