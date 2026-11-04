package gm

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/spelech/gmessage-rag/internal/db"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
)

// MessageFetcher abstracts libgm message pagination for testability.
type MessageFetcher interface {
	FetchMessages(ctx context.Context, conversationID string, count int64, cursor []byte) (*gmproto.ListMessagesResponse, error)
}

// BackfillOptions configures the pagination loop.
type BackfillOptions struct {
	MaxMessages int
	PageSize    int64
	Delay       time.Duration
}

// DefaultBackfillOptions provides sensible defaults.
var DefaultBackfillOptions = BackfillOptions{
	MaxMessages: 1000,
	PageSize:    100,
	Delay:       200 * time.Millisecond,
}

// BackfillConversation paginates historical messages for a tracked conversation
// using cursor pagination, normalizes timestamps, and batch-upserts into the database.
func BackfillConversation(ctx context.Context, fetcher MessageFetcher, database *db.DB, conversationID string, opts BackfillOptions) error {
	if fetcher == nil {
		return errors.New("fetcher cannot be nil")
	}
	if database == nil {
		return errors.New("database cannot be nil")
	}
	if conversationID == "" {
		return errors.New("conversationID cannot be empty")
	}

	if opts.MaxMessages <= 0 {
		opts.MaxMessages = DefaultBackfillOptions.MaxMessages
	}
	if opts.PageSize <= 0 {
		opts.PageSize = DefaultBackfillOptions.PageSize
	}
	if opts.Delay <= 0 {
		opts.Delay = DefaultBackfillOptions.Delay
	}

	// 1. Check if backfill is already completed
	conv, err := database.GetConversation(ctx, conversationID)
	if err != nil && !errors.Is(err, db.ErrNotFound) {
		return fmt.Errorf("check conversation backfill status: %w", err)
	}
	if conv != nil && conv.BackfillComplete {
		// Already fully backfilled, no-op
		return nil
	}

	var cursor []byte
	if conv != nil && conv.BackfillCursor != nil && *conv.BackfillCursor != "" {
		cursor = []byte(*conv.BackfillCursor)
	}

	totalFetched := 0

	for totalFetched < opts.MaxMessages {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		resp, err := fetcher.FetchMessages(ctx, conversationID, opts.PageSize, cursor)
		if err != nil {
			return fmt.Errorf("fetch messages page for conv %s: %w", conversationID, err)
		}
		if resp == nil {
			break
		}

		messages := resp.GetMessages()
		if len(messages) == 0 {
			// No more messages available
			break
		}

		var dbMsgs []*db.Message
		for _, rawMsg := range messages {
			if dbMsg := ProtoMessageToDBMessage(rawMsg, conversationID, true); dbMsg != nil {
				dbMsgs = append(dbMsgs, dbMsg)
			}
		}

		if len(dbMsgs) > 0 {
			if _, err := database.UpsertMessagesBatch(ctx, dbMsgs); err != nil {
				return fmt.Errorf("upsert batch of %d messages: %w", len(dbMsgs), err)
			}
			totalFetched += len(dbMsgs)
		}

		// Update cursor for next page
		respCursor := resp.GetCursor()
		if respCursor == nil || respCursor.GetLastItemID() == "" {
			// Reached end of historical timeline
			break
		}

		cursor = []byte(respCursor.GetLastItemID())
		cursorStr := string(cursor)

		// Persist cursor progression on conversation
		if conv == nil {
			conv = &db.Conversation{
				ID:             conversationID,
				BackfillCursor: &cursorStr,
			}
		} else {
			conv.BackfillCursor = &cursorStr
		}
		if err := database.UpsertConversation(ctx, conv); err != nil {
			log.Warn().Err(err).Str("conv_id", conversationID).Msg("Failed to persist backfill cursor")
		}

		// Politeness delay
		if opts.Delay > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(opts.Delay):
			}
		}
	}

	// Conclude backfill
	if conv == nil {
		conv = &db.Conversation{
			ID:               conversationID,
			BackfillComplete: true,
		}
	} else {
		conv.BackfillComplete = true
	}

	if err := database.UpsertConversation(ctx, conv); err != nil {
		return fmt.Errorf("mark conversation backfill_complete: %w", err)
	}

	return nil
}
