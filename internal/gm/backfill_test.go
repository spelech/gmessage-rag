package gm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/spelech/gmessage-rag/internal/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
)

type mockMessageFetcher struct {
	pages       map[string]*gmproto.ListMessagesResponse
	fetchErr    error
	fetchErrKey string
}

func (m *mockMessageFetcher) FetchMessages(ctx context.Context, conversationID string, count int64, cursor []byte) (*gmproto.ListMessagesResponse, error) {
	key := string(cursor)
	if m.fetchErr != nil && (m.fetchErrKey == "" || m.fetchErrKey == key) {
		return nil, m.fetchErr
	}
	resp, ok := m.pages[key]
	if !ok {
		return &gmproto.ListMessagesResponse{}, nil
	}
	return resp, nil
}

func getTestDB(t *testing.T) *db.DB {
	t.Helper()
	testDBURL := os.Getenv("DATABASE_URL")
	if testDBURL == "" {
		testDBURL = "postgres://jules:@127.0.0.1:5432/gmessages?sslmode=disable"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	database, err := db.Connect(ctx, testDBURL)
	if err != nil {
		t.Skipf("skipping test: database connection failed: %v", err)
		return nil
	}
	return database
}

func TestBackfill_BoundaryAndCompletion(t *testing.T) {
	database := getTestDB(t)
	if database == nil {
		return
	}
	defer database.Close()
	ctx := context.Background()

	convID := fmt.Sprintf("test-conv-backfill-%d", time.Now().UnixNano())
	conv := &db.Conversation{
		ID:        convID,
		Name:      "Backfill Test Contact",
		IsTracked: true,
	}
	if err := database.UpsertConversation(ctx, conv); err != nil {
		t.Fatalf("failed to seed conversation: %v", err)
	}

	// Prepare 2 pages of messages
	fetcher := &mockMessageFetcher{
		pages: map[string]*gmproto.ListMessagesResponse{
			"": {
				Messages: []*gmproto.Message{
					{
						MessageID:      "msg-page1-01",
						ConversationID: convID,
						Timestamp:      time.Now().Add(-2 * time.Hour).UnixMicro(),
						MessageInfo: []*gmproto.MessageInfo{
							{
								Data: &gmproto.MessageInfo_MessageContent{
									MessageContent: &gmproto.MessageContent{Content: "Historical message 1"},
								},
							},
						},
					},
				},
				Cursor: &gmproto.Cursor{
					LastItemID: "cursor-page-2",
				},
			},
			"cursor-page-2": {
				Messages: []*gmproto.Message{
					{
						MessageID:      "msg-page2-01",
						ConversationID: convID,
						Timestamp:      time.Now().Add(-4 * time.Hour).UnixMicro(),
						MessageInfo: []*gmproto.MessageInfo{
							{
								Data: &gmproto.MessageInfo_MessageContent{
									MessageContent: &gmproto.MessageContent{Content: "Historical message 2"},
								},
							},
						},
					},
				},
				Cursor: &gmproto.Cursor{
					LastItemID: "", // End of history
				},
			},
		},
	}

	opts := BackfillOptions{
		MaxMessages: 10,
		PageSize:    1,
		Delay:       10 * time.Millisecond,
	}

	// 1. Run backfill
	if err := BackfillConversation(ctx, fetcher, database, convID, opts); err != nil {
		t.Fatalf("BackfillConversation failed: %v", err)
	}

	// 2. Assert conversation state marked backfill_complete = true
	updatedConv, err := database.GetConversation(ctx, convID)
	if err != nil {
		t.Fatalf("failed to get updated conversation: %v", err)
	}
	if !updatedConv.BackfillComplete {
		t.Errorf("expected backfill_complete to be true")
	}

	// 3. Second run should be no-op
	if err := BackfillConversation(ctx, fetcher, database, convID, opts); err != nil {
		t.Fatalf("second backfill call failed: %v", err)
	}
}

func TestBackfill_ValidationErrors(t *testing.T) {
	database := getTestDB(t)
	if database == nil {
		return
	}
	defer database.Close()
	ctx := context.Background()
	fetcher := &mockMessageFetcher{}

	// 1. Nil fetcher
	err := BackfillConversation(ctx, nil, database, "conv-1", BackfillOptions{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "fetcher cannot be nil")

	// 2. Nil database
	err = BackfillConversation(ctx, fetcher, nil, "conv-1", BackfillOptions{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "database cannot be nil")

	// 3. Empty conversation ID
	err = BackfillConversation(ctx, fetcher, database, "", BackfillOptions{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "conversationID cannot be empty")
}

func TestBackfill_CancellationAndFetchError(t *testing.T) {
	database := getTestDB(t)
	if database == nil {
		return
	}
	defer database.Close()

	convID := fmt.Sprintf("test-conv-cancel-%d", time.Now().UnixNano())
	fetcher := &mockMessageFetcher{
		fetchErr: errors.New("network failure while fetching messages"),
	}

	// 1. Immediate cancel
	cancelCtx, cancel := context.WithCancel(context.Background())
	cancel()
	err := BackfillConversation(cancelCtx, fetcher, database, convID, BackfillOptions{
		MaxMessages: 10,
		PageSize:    5,
	})
	assert.Error(t, err)

	// 2. Fetch error
	err = BackfillConversation(context.Background(), fetcher, database, convID, BackfillOptions{
		MaxMessages: 10,
		PageSize:    5,
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "network failure while fetching messages")
}

func TestBackfill_RichMessageParsing(t *testing.T) {
	database := getTestDB(t)
	if database == nil {
		return
	}
	defer database.Close()
	ctx := context.Background()

	convID := fmt.Sprintf("test-conv-rich-%d", time.Now().UnixNano())
	conv := &db.Conversation{
		ID:        convID,
		Name:      "Rich Test Contact",
		IsTracked: true,
	}
	require.NoError(t, database.UpsertConversation(ctx, conv))

	replyID := "msg-parent-001"
	senderName := "Alice Bob"

	fetcher := &mockMessageFetcher{
		pages: map[string]*gmproto.ListMessagesResponse{
			"": {
				Messages: []*gmproto.Message{
					nil, // Nil message should be skipped safely
					{
						MessageID:      "msg-rich-001",
						ConversationID: convID,
						Timestamp:      time.Now().Add(-1 * time.Hour).UnixMicro(),
						MessageStatus: &gmproto.MessageStatus{
							Status: gmproto.MessageStatusType_OUTGOING_DELIVERED,
						},
						ReplyMessage: &gmproto.ReplyMessage{
							MessageID: replyID,
						},
						SenderParticipant: &gmproto.Participant{
							FullName: senderName,
						},
						Reactions: []*gmproto.ReactionEntry{
							nil, // Nil reaction entry
							{
								Data: &gmproto.ReactionData{
									Unicode: "❤️",
								},
								ParticipantIDs: []string{"participant-1"},
							},
						},
						MessageInfo: []*gmproto.MessageInfo{
							{
								Data: &gmproto.MessageInfo_MessageContent{
									MessageContent: &gmproto.MessageContent{Content: "Rich historical content"},
								},
							},
						},
					},
				},
				Cursor: &gmproto.Cursor{
					LastItemID: "",
				},
			},
		},
	}

	// Test default options fallback (passing 0 for MaxMessages, PageSize, Delay)
	err := BackfillConversation(ctx, fetcher, database, convID, BackfillOptions{})
	require.NoError(t, err)

	// Verify message in database
	messages, err := database.GetUnassignedMessages(ctx, convID)
	require.NoError(t, err)
	require.Len(t, messages, 1)
	assert.Equal(t, "msg-rich-001", messages[0].ID)
	assert.True(t, messages[0].IsFromMe)
	assert.Equal(t, &replyID, messages[0].ReplyToMessageID)
	assert.Len(t, messages[0].Reactions, 1)
	assert.Equal(t, "❤️", messages[0].Reactions[0].Emoji)
}
