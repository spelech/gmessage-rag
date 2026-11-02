package gm

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/spelech/gmessage-rag/internal/db"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
)

type mockMessageFetcher struct {
	pages map[string]*gmproto.ListMessagesResponse
}

func (m *mockMessageFetcher) FetchMessages(ctx context.Context, conversationID string, count int64, cursor []byte) (*gmproto.ListMessagesResponse, error) {
	key := string(cursor)
	resp, ok := m.pages[key]
	if !ok {
		return &gmproto.ListMessagesResponse{}, nil
	}
	return resp, nil
}

func TestBackfill_BoundaryAndCompletion(t *testing.T) {
	// Skip if test postgres DB is not available
	testDBURL := "postgres://jules:gmessages_dev@localhost:5432/gmessages?sslmode=disable"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	database, err := db.Connect(ctx, testDBURL)
	if err != nil {
		t.Skipf("skipping test: database connection failed: %v", err)
		return
	}
	defer database.Close()

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
