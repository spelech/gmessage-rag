package pipeline

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/spelech/gmessage-rag/internal/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
)

type mockFetcher struct {
	fetchFn func(ctx context.Context, conversationID string, count int64, cursor []byte) (*gmproto.ListMessagesResponse, error)
}

func (m *mockFetcher) FetchMessages(ctx context.Context, conversationID string, count int64, cursor []byte) (*gmproto.ListMessagesResponse, error) {
	if m.fetchFn != nil {
		return m.fetchFn(ctx, conversationID, count, cursor)
	}
	return &gmproto.ListMessagesResponse{}, nil
}

func TestETLPipeline_Transform(t *testing.T) {
	p := &ETLPipeline{}

	t.Run("Empty input", func(t *testing.T) {
		msgs := p.Transform(nil, "conv-1")
		assert.Empty(t, msgs)

		msgs2 := p.Transform([]*gmproto.Message{}, "conv-1")
		assert.Empty(t, msgs2)
	})

	t.Run("Edge cases and full fields", func(t *testing.T) {
		statusIncoming := gmproto.MessageStatusType_INCOMING_DELIVERED // 100+
		statusOutgoing := gmproto.MessageStatusType_OUTGOING_DELIVERED // 0 <= code < 100

		replyMsgID := "reply-to-123"
		microTS := int64(1700000000123456) // microseconds (>10^14)
		milliTS := int64(1700000000123)    // milliseconds (>10^11)

		raw := []*gmproto.Message{
			nil, // Should be skipped
			{
				MessageID:      "msg-1",
				ConversationID: "conv-1",
				ParticipantID:  "sender-abc",
				SenderParticipant: &gmproto.Participant{
					FullName: "Alice",
				},
				Timestamp: microTS,
				MessageStatus: &gmproto.MessageStatus{
					Status: statusIncoming,
				},
				MessageInfo: []*gmproto.MessageInfo{
					{
						Data: &gmproto.MessageInfo_MessageContent{
							MessageContent: &gmproto.MessageContent{Content: "Hello from Alice"},
						},
					},
				},
				ReplyMessage: &gmproto.ReplyMessage{
					MessageID: replyMsgID,
				},
				Reactions: []*gmproto.ReactionEntry{
					nil,
					{
						Data: &gmproto.ReactionData{
							Unicode: "❤️",
						},
						ParticipantIDs: []string{"sender-bob"},
					},
					{
						Data: &gmproto.ReactionData{
							Unicode: "", // Empty unicode ignored
						},
					},
				},
			},
			{
				MessageID:      "msg-2",
				ConversationID: "conv-1",
				ParticipantID:  "sender-xyz",
				SenderParticipant: &gmproto.Participant{
					FullName: "", // Fallback to "me" because outgoing
				},
				Timestamp: milliTS,
				MessageStatus: &gmproto.MessageStatus{
					Status: statusOutgoing,
				},
				MessageInfo: []*gmproto.MessageInfo{}, // empty message info
			},
			{
				MessageID:         "msg-3",
				ConversationID:    "conv-1",
				ParticipantID:     "contact-foo",
				SenderParticipant: nil,        // Fallback to ParticipantID
				Timestamp:         1700000000, // seconds
				MessageStatus: &gmproto.MessageStatus{
					Status: statusIncoming,
				},
			},
		}

		transformed := p.Transform(raw, "conv-1")
		require.Len(t, transformed, 3)

		// msg-1 assertions
		m1 := transformed[0]
		assert.Equal(t, "msg-1", m1.ID)
		assert.Equal(t, "conv-1", m1.ConversationID)
		assert.Equal(t, "sender-abc", m1.SenderID)
		assert.Equal(t, "Alice", m1.SenderName)
		assert.False(t, m1.IsFromMe)
		assert.True(t, m1.IsBackfill)
		assert.Equal(t, "Hello from Alice", m1.Body)
		require.NotNil(t, m1.ReplyToMessageID)
		assert.Equal(t, replyMsgID, *m1.ReplyToMessageID)
		require.Len(t, m1.Reactions, 1)
		assert.Equal(t, "sender-bob", m1.Reactions[0].Sender)
		assert.Equal(t, "❤️", m1.Reactions[0].Emoji)
		assert.Equal(t, time.Unix(1700000000, 123456*1000).UTC(), m1.SentAt)

		// msg-2 assertions
		m2 := transformed[1]
		assert.Equal(t, "msg-2", m2.ID)
		assert.Equal(t, "me", m2.SenderID)
		assert.Equal(t, "me", m2.SenderName)
		assert.True(t, m2.IsFromMe)
		assert.True(t, m2.IsBackfill)
		assert.Equal(t, "", m2.Body)
		assert.Nil(t, m2.ReplyToMessageID)
		assert.Empty(t, m2.Reactions)
		assert.Equal(t, time.Unix(1700000000, 123*1000000).UTC(), m2.SentAt)

		// msg-3 assertions
		m3 := transformed[2]
		assert.Equal(t, "msg-3", m3.ID)
		assert.Equal(t, "contact-foo", m3.SenderID)
		assert.Equal(t, "contact-foo", m3.SenderName)
		assert.False(t, m3.IsFromMe)
		assert.Equal(t, time.Unix(1700000000, 0).UTC(), m3.SentAt)
	})
}

func TestETLPipeline_Extract(t *testing.T) {
	ctx := context.Background()

	t.Run("Nil fetcher validation", func(t *testing.T) {
		p := &ETLPipeline{Fetcher: nil}
		_, _, err := p.Extract(ctx, "conv-1", 50, nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "fetcher cannot be nil")
	})

	t.Run("Empty conversationID validation", func(t *testing.T) {
		p := &ETLPipeline{Fetcher: &mockFetcher{}}
		_, _, err := p.Extract(ctx, "", 50, nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "conversationID cannot be empty")
	})

	t.Run("Fetcher error propagation", func(t *testing.T) {
		expectedErr := errors.New("network disconnect")
		p := &ETLPipeline{
			Fetcher: &mockFetcher{
				fetchFn: func(ctx context.Context, conversationID string, count int64, cursor []byte) (*gmproto.ListMessagesResponse, error) {
					return nil, expectedErr
				},
			},
		}

		msgs, cursor, err := p.Extract(ctx, "conv-1", 50, []byte("c1"))
		assert.Nil(t, msgs)
		assert.Nil(t, cursor)
		assert.ErrorIs(t, err, expectedErr)
	})

	t.Run("Nil response from fetcher", func(t *testing.T) {
		p := &ETLPipeline{
			Fetcher: &mockFetcher{
				fetchFn: func(ctx context.Context, conversationID string, count int64, cursor []byte) (*gmproto.ListMessagesResponse, error) {
					return nil, nil
				},
			},
		}

		msgs, cursor, err := p.Extract(ctx, "conv-1", 50, nil)
		assert.NoError(t, err)
		assert.Nil(t, msgs)
		assert.Nil(t, cursor)
	})

	t.Run("Valid response with cursor", func(t *testing.T) {
		expectedMsgs := []*gmproto.Message{
			{MessageID: "msg-1"},
			{MessageID: "msg-2"},
		}
		p := &ETLPipeline{
			Fetcher: &mockFetcher{
				fetchFn: func(ctx context.Context, conversationID string, count int64, cursor []byte) (*gmproto.ListMessagesResponse, error) {
					assert.Equal(t, "conv-1", conversationID)
					assert.Equal(t, int64(10), count)
					assert.Equal(t, "cursor-prev", string(cursor))
					return &gmproto.ListMessagesResponse{
						Messages: expectedMsgs,
						Cursor: &gmproto.Cursor{
							LastItemID: "cursor-next",
						},
					}, nil
				},
			},
		}

		msgs, cursor, err := p.Extract(ctx, "conv-1", 10, []byte("cursor-prev"))
		assert.NoError(t, err)
		assert.Equal(t, expectedMsgs, msgs)
		assert.Equal(t, []byte("cursor-next"), cursor)
	})
}

func TestETLPipeline_LoadValidation(t *testing.T) {
	ctx := context.Background()

	t.Run("Nil database validation", func(t *testing.T) {
		p := &ETLPipeline{Database: nil}
		err := p.Load(ctx, "conv-1", []*db.Message{}, nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "database cannot be nil")
	})

	t.Run("Empty conversationID validation", func(t *testing.T) {
		p := &ETLPipeline{Database: &db.DB{}}
		err := p.Load(ctx, "", []*db.Message{}, nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "conversationID cannot be empty")
	})

	t.Run("Database pool nil fails cleanly", func(t *testing.T) {
		p := &ETLPipeline{Database: &db.DB{Pool: nil}}
		err := p.Load(ctx, "conv-1", []*db.Message{}, []byte("cur-1"))
		assert.Error(t, err)
	})
}

func TestETLPipeline_RunBatch_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	p := &ETLPipeline{
		Fetcher:  &mockFetcher{},
		Database: &db.DB{},
	}

	nextCursor, count, err := p.RunBatch(ctx, "conv-1", 50, nil)
	assert.ErrorIs(t, err, context.Canceled)
	assert.Nil(t, nextCursor)
	assert.Equal(t, 0, count)
}

func TestETLPipeline_RunBatch_ExtractFails(t *testing.T) {
	fetchErr := errors.New("extract failed")
	p := &ETLPipeline{
		Fetcher: &mockFetcher{
			fetchFn: func(ctx context.Context, conversationID string, count int64, cursor []byte) (*gmproto.ListMessagesResponse, error) {
				return nil, fetchErr
			},
		},
		Database: &db.DB{},
	}

	nextCursor, count, err := p.RunBatch(context.Background(), "conv-1", 50, nil)
	assert.ErrorIs(t, err, fetchErr)
	assert.Nil(t, nextCursor)
	assert.Equal(t, 0, count)
}

// TestETLPipeline_BatchProcessing verifies nominal execution against a real database (DB-01 / PIPE-06).
func TestETLPipeline_BatchProcessing(t *testing.T) {
	testDBURL := "postgres://jules:gmessages_dev@localhost:5432/gmessages?sslmode=disable"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	database, err := db.Connect(ctx, testDBURL)
	if err != nil {
		t.Skipf("skipping test: database connection failed: %v", err)
		return
	}
	defer database.Close()

	convID := fmt.Sprintf("test-conv-etl-%d", time.Now().UnixNano())
	conv := &db.Conversation{
		ID:        convID,
		Name:      "ETL Pipeline Test Contact",
		IsTracked: true,
	}
	if err := database.UpsertConversation(ctx, conv); err != nil {
		t.Fatalf("failed to seed conversation: %v", err)
	}

	pages := map[string]*gmproto.ListMessagesResponse{
		"": {
			Messages: []*gmproto.Message{
				{
					MessageID:      "etl-msg-01",
					ConversationID: convID,
					Timestamp:      time.Now().Add(-1 * time.Hour).UnixMicro(),
					ParticipantID:  "Alice",
					MessageInfo: []*gmproto.MessageInfo{
						{
							Data: &gmproto.MessageInfo_MessageContent{
								MessageContent: &gmproto.MessageContent{Content: "ETL Batch 1 Message"},
							},
						},
					},
				},
			},
			Cursor: &gmproto.Cursor{
				LastItemID: "cursor-etl-2",
			},
		},
		"cursor-etl-2": {
			Messages: []*gmproto.Message{
				{
					MessageID:      "etl-msg-02",
					ConversationID: convID,
					Timestamp:      time.Now().Add(-2 * time.Hour).UnixMicro(),
					ParticipantID:  "Bob",
					MessageInfo: []*gmproto.MessageInfo{
						{
							Data: &gmproto.MessageInfo_MessageContent{
								MessageContent: &gmproto.MessageContent{Content: "ETL Batch 2 Message"},
							},
						},
					},
				},
			},
			Cursor: &gmproto.Cursor{
				LastItemID: "",
			},
		},
	}

	fetcher := &mockFetcher{
		fetchFn: func(ctx context.Context, conversationID string, count int64, cursor []byte) (*gmproto.ListMessagesResponse, error) {
			key := string(cursor)
			resp, ok := pages[key]
			if !ok {
				return &gmproto.ListMessagesResponse{}, nil
			}
			return resp, nil
		},
	}

	pipeline := &ETLPipeline{
		Fetcher:  fetcher,
		Database: database,
	}

	// 1. Run first batch
	nextCursor, inserted, err := pipeline.RunBatch(ctx, convID, 10, nil)
	require.NoError(t, err)
	assert.Equal(t, 1, inserted)
	assert.Equal(t, []byte("cursor-etl-2"), nextCursor)

	// Check DB cursor updated
	c1, err := database.GetConversation(ctx, convID)
	require.NoError(t, err)
	require.NotNil(t, c1.BackfillCursor)
	assert.Equal(t, "cursor-etl-2", *c1.BackfillCursor)

	// 2. Run second batch
	nextCursor2, inserted2, err := pipeline.RunBatch(ctx, convID, 10, nextCursor)
	require.NoError(t, err)
	assert.Equal(t, 1, inserted2)
	assert.Empty(t, nextCursor2)

	// Check DB cursor updated to empty/nil
	c2, err := database.GetConversation(ctx, convID)
	require.NoError(t, err)
	assert.Nil(t, c2.BackfillCursor)
}
