package web_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spelech/gmessage-rag/internal/db"
	"github.com/spelech/gmessage-rag/internal/gm"
	"github.com/spelech/gmessage-rag/internal/web"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
)

// fakeWebSender records sends without a live Google connection.
type fakeWebSender struct {
	mu            sync.Mutex
	textCalls     []gm.OutgoingMessage
	mediaCalls    []gm.OutgoingMedia
	reactionCalls []fakeWebReactionCall
	err           error
}

type fakeWebReactionCall struct {
	MessageID string
	Emoji     string
	Remove    bool
}

func (f *fakeWebSender) SendText(_ context.Context, msg gm.OutgoingMessage) (*gm.SendResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.textCalls = append(f.textCalls, msg)
	if f.err != nil {
		return nil, f.err
	}
	return &gm.SendResult{
		TmpID:          "tmp-web-test",
		ConversationID: msg.ConversationID,
		SentAt:         time.Now().UTC(),
		Status:         "SUCCESS",
	}, nil
}

func (f *fakeWebSender) SendMedia(_ context.Context, msg gm.OutgoingMedia) (*gm.SendResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mediaCalls = append(f.mediaCalls, msg)
	if f.err != nil {
		return nil, f.err
	}
	return &gm.SendResult{
		TmpID:          "tmp-web-test",
		ConversationID: msg.ConversationID,
		SentAt:         time.Now().UTC(),
		Status:         "SUCCESS",
		MediaID:        "media-web-test",
	}, nil
}

func (f *fakeWebSender) SendReaction(_ context.Context, messageID, emoji string, remove bool, _ *gmproto.SIMPayload) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reactionCalls = append(f.reactionCalls, fakeWebReactionCall{MessageID: messageID, Emoji: emoji, Remove: remove})
	return f.err
}

func openSendTestDB(t *testing.T) *db.DB {
	t.Helper()

	connStr := os.Getenv("DATABASE_URL")
	if connStr == "" {
		connStr = "postgres://jules:postgres@localhost:5432/gmessages?sslmode=disable"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	database, err := db.New(ctx, connStr)
	if err != nil {
		t.Skipf("skipping send test: database connection failed: %v", err)
	}
	if err := database.Migrate(ctx); err != nil {
		database.Close()
		t.Fatalf("database migration failed: %v", err)
	}
	t.Cleanup(database.Close)
	return database
}

func newSendMux(sender web.MessageSender, database *db.DB, enabled bool) *http.ServeMux {
	mux := http.NewServeMux()
	web.NewSendHandler(sender, database, enabled).RegisterRoutes(mux)
	return mux
}

func TestSendHandler_Disabled(t *testing.T) {
	mux := newSendMux(nil, nil, false)

	for _, path := range []string{"/api/v1/send", "/api/v1/send/media"} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"conversation_id":"conv","text":"hi"}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusForbidden, rec.Code, path)
		assert.Contains(t, rec.Body.String(), "outbound send is disabled")
	}
}

func TestSendHandler_Text(t *testing.T) {
	sender := &fakeWebSender{}
	mux := newSendMux(sender, nil, true)

	body := `{"conversation_id":"conv-text","text":"hello web","reply_to_message_id":"reply-1"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/send", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "tmp-web-test")
	require.Len(t, sender.textCalls, 1)
	assert.Equal(t, "conv-text", sender.textCalls[0].ConversationID)
	assert.Equal(t, "hello web", sender.textCalls[0].Text)
	assert.Equal(t, "reply-1", sender.textCalls[0].ReplyToID)
}

func TestSendHandler_TextValidation(t *testing.T) {
	sender := &fakeWebSender{}
	mux := newSendMux(sender, nil, true)

	tests := []struct {
		name string
		body string
	}{
		{"missing conversation", `{"text":"hi"}`},
		{"missing text", `{"conversation_id":"conv"}`},
		{"both text and reaction", `{"conversation_id":"conv","text":"hi","reaction_message_id":"m1"}`},
		{"invalid json", `{`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/send", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
		})
	}
	assert.Empty(t, sender.textCalls)
}

func TestSendHandler_ReactionPersists(t *testing.T) {
	database := openSendTestDB(t)
	sender := &fakeWebSender{}
	mux := newSendMux(sender, database, true)
	ctx := context.Background()

	convID := fmt.Sprintf("conv-send-%d", time.Now().UnixNano())
	require.NoError(t, database.UpsertConversation(ctx, &db.Conversation{
		ID:   convID,
		Name: "Send Reaction Test",
	}))
	msgID := fmt.Sprintf("msg-send-%d", time.Now().UnixNano())
	require.NoError(t, database.UpsertMessage(ctx, &db.Message{
		ID:             msgID,
		ConversationID: convID,
		SenderID:       "sender",
		SenderName:     "Sender",
		SentAt:         time.Now().UTC(),
		Body:           "react to me",
	}))

	body := fmt.Sprintf(`{"conversation_id":%q,"reaction_message_id":%q,"reaction_emoji":"👍"}`, convID, msgID)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/send", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Len(t, sender.reactionCalls, 1)
	assert.Equal(t, msgID, sender.reactionCalls[0].MessageID)
	assert.Equal(t, "👍", sender.reactionCalls[0].Emoji)
	assert.False(t, sender.reactionCalls[0].Remove)

	// Verify the reaction was persisted to the real database.
	var reactions []db.Reaction
	require.NoError(t, database.Pool.QueryRow(ctx, `SELECT reactions FROM messages WHERE id = $1`, msgID).Scan(&reactions))
	require.Len(t, reactions, 1)
	assert.Equal(t, "me", reactions[0].Sender)
	assert.Equal(t, "👍", reactions[0].Emoji)

	// Remove it through the API and verify the row is updated.
	body = fmt.Sprintf(`{"conversation_id":%q,"reaction_message_id":%q,"reaction_emoji":"👍","reaction_remove":true}`, convID, msgID)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/send", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	require.NoError(t, database.Pool.QueryRow(ctx, `SELECT reactions FROM messages WHERE id = $1`, msgID).Scan(&reactions))
	assert.Empty(t, reactions)
}

func TestSendHandler_ReactionRequiresDatabase(t *testing.T) {
	sender := &fakeWebSender{}
	mux := newSendMux(sender, nil, true)

	body := `{"conversation_id":"conv","reaction_message_id":"msg","reaction_emoji":"👍"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/send", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Contains(t, rec.Body.String(), "database is not configured")
}

func TestSendHandler_MediaMultipart(t *testing.T) {
	sender := &fakeWebSender{}
	mux := newSendMux(sender, nil, true)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("conversation_id", "conv-media"))
	require.NoError(t, writer.WriteField("caption", "receipt"))
	require.NoError(t, writer.WriteField("reply_to_message_id", "reply-2"))
	part, err := writer.CreateFormFile("file", "receipt.png")
	require.NoError(t, err)
	_, err = part.Write([]byte("fake-image-bytes"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/send/media", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "media-web-test")
	require.Len(t, sender.mediaCalls, 1)
	assert.Equal(t, "conv-media", sender.mediaCalls[0].ConversationID)
	assert.Equal(t, "receipt.png", sender.mediaCalls[0].FileName)
	assert.Equal(t, "receipt", sender.mediaCalls[0].Caption)
	assert.Equal(t, "reply-2", sender.mediaCalls[0].ReplyToID)
	assert.Equal(t, []byte("fake-image-bytes"), sender.mediaCalls[0].Data)
}

func TestSendHandler_MediaOversized(t *testing.T) {
	sender := &fakeWebSender{}
	h := web.NewSendHandler(sender, nil, true)
	h.MaxMediaBytes = 4
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("conversation_id", "conv-media"))
	part, err := writer.CreateFormFile("file", "big.bin")
	require.NoError(t, err)
	_, err = part.Write([]byte("too-large"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/send/media", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
	assert.Contains(t, rec.Body.String(), "byte limit")
	assert.Empty(t, sender.mediaCalls)
}

func TestSendHandler_MediaMissingFile(t *testing.T) {
	sender := &fakeWebSender{}
	mux := newSendMux(sender, nil, true)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("conversation_id", "conv-media"))
	require.NoError(t, writer.Close())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/send/media", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "field 'file' is required")
}

func TestStatus_SendEnabled(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(fmt.Sprintf("enabled=%v", enabled), func(t *testing.T) {
			handler := web.NewAPIHandlerWithSend(nil, nil, nil, enabled)
			req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
			rec := httptest.NewRecorder()
			handler.HandleStatus(rec, req)

			require.Equal(t, http.StatusOK, rec.Code)
			var resp struct {
				SendEnabled bool `json:"send_enabled"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
			assert.Equal(t, enabled, resp.SendEnabled)
		})
	}
}
