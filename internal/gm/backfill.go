package gm

import (
	"context"
	"errors"
	"fmt"
	"time"

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
			if rawMsg == nil {
				continue
			}

			statusCode := int32(0)
			if rawMsg.GetMessageStatus() != nil {
				statusCode = int32(rawMsg.GetMessageStatus().GetStatus())
			}
			isFromMe, _ := ResolveSenderDirection(statusCode)
			sentAt := NormalizeTimestamp(rawMsg.GetTimestamp())

			var bodyText string
			for _, info := range rawMsg.GetMessageInfo() {
				if mc := info.GetMessageContent(); mc != nil {
					bodyText = mc.GetContent()
					break
				}
			}

			var replyToID *string
			if replyMsg := rawMsg.GetReplyMessage(); replyMsg != nil && replyMsg.GetMessageID() != "" {
				rID := replyMsg.GetMessageID()
				replyToID = &rID
			}

			var reactions []db.Reaction
			for _, rEntry := range rawMsg.GetReactions() {
				if rEntry == nil || rEntry.GetData() == nil {
					continue
				}
				emoji := rEntry.GetData().GetUnicode()
				sender := ""
				if len(rEntry.GetParticipantIDs()) > 0 {
					sender = rEntry.GetParticipantIDs()[0]
				}
				if emoji != "" {
					reactions = append(reactions, db.Reaction{
						Sender: sender,
						Emoji:  emoji,
					})
				}
			}

			senderID := rawMsg.GetParticipantID()
			if isFromMe {
				senderID = "me"
			}
			senderName := senderID
			if rawMsg.GetSenderParticipant() != nil && rawMsg.GetSenderParticipant().GetFullName() != "" {
				senderName = rawMsg.GetSenderParticipant().GetFullName()
			}

			statusVal := statusCode
			dbMsg := &db.Message{
				ID:               rawMsg.GetMessageID(),
				ConversationID:   conversationID,
				SenderID:         senderID,
				SenderName:       senderName,
				IsFromMe:         isFromMe,
				SentAt:           sentAt,
				Body:             bodyText,
				ReplyToMessageID: replyToID,
				Reactions:        reactions,
				RawStatus:        &statusVal,
				IsBackfill:       true,
			}
			dbMsgs = append(dbMsgs, dbMsg)
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
		_ = database.UpsertConversation(ctx, conv)

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
