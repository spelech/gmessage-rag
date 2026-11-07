package mcpserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spelech/gmessage-rag/internal/db"
	"github.com/spelech/gmessage-rag/internal/gm"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
)

// fakeSender records outgoing sends and returns canned results. It satisfies
// MessageSender without a live Google connection.
type fakeSender struct {
	mu            sync.Mutex
	textCalls     []gm.OutgoingMessage
	mediaCalls    []gm.OutgoingMedia
	reactionCalls []fakeReactionCall
	err           error
}

type fakeReactionCall struct {
	MessageID string
	Emoji     string
	Remove    bool
}

func (f *fakeSender) SendText(_ context.Context, msg gm.OutgoingMessage) (*gm.SendResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.textCalls = append(f.textCalls, msg)
	if f.err != nil {
		return nil, f.err
	}
	return &gm.SendResult{
		TmpID:          "tmp-test",
		ConversationID: msg.ConversationID,
		SentAt:         time.Now().UTC(),
		Status:         "SUCCESS",
	}, nil
}

func (f *fakeSender) SendMedia(_ context.Context, msg gm.OutgoingMedia) (*gm.SendResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mediaCalls = append(f.mediaCalls, msg)
	if f.err != nil {
		return nil, f.err
	}
	return &gm.SendResult{
		TmpID:          "tmp-test",
		ConversationID: msg.ConversationID,
		SentAt:         time.Now().UTC(),
		Status:         "SUCCESS",
		MediaID:        "media-test",
	}, nil
}

func (f *fakeSender) SendReaction(_ context.Context, messageID, emoji string, remove bool, _ *gmproto.SIMPayload) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reactionCalls = append(f.reactionCalls, fakeReactionCall{MessageID: messageID, Emoji: emoji, Remove: remove})
	return f.err
}

// sendMessageToolNames returns the registered tool names for a server.
func sendMessageToolNames(t *testing.T, srv *Server) map[string]bool {
	t.Helper()
	resp := srv.HandleMessage(context.Background(), &JSONRPCMessage{
		JSONRPC: "2.0",
		ID:      1,
		Method:  MethodToolsList,
	})
	if resp == nil || resp.Error != nil {
		t.Fatalf("tools/list failed: %+v", resp)
	}
	resultMap, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("expected result map, got %T", resp.Result)
	}
	tools, ok := resultMap["tools"].([]Tool)
	if !ok {
		t.Fatalf("expected []Tool, got %T", resultMap["tools"])
	}
	names := make(map[string]bool, len(tools))
	for _, tool := range tools {
		names[tool.Name] = true
	}
	return names
}

// callToolRaw dispatches tools/call and returns the raw result without failing
// on error results. Used to assert validation failures.
func callToolRaw(t *testing.T, srv *Server, name string, args any) *CallToolResult {
	t.Helper()
	rawArgs := json.RawMessage("{}")
	if args != nil {
		encoded, err := json.Marshal(args)
		if err != nil {
			t.Fatalf("marshal arguments for %s: %v", name, err)
		}
		rawArgs = encoded
	}
	params, err := json.Marshal(CallToolParams{Name: name, Arguments: rawArgs})
	if err != nil {
		t.Fatalf("marshal call params for %s: %v", name, err)
	}
	resp := srv.HandleMessage(context.Background(), &JSONRPCMessage{
		JSONRPC: "2.0",
		ID:      "test",
		Method:  MethodToolsCall,
		Params:  params,
	})
	if resp == nil {
		t.Fatalf("tool %s returned nil response", name)
	}
	result, ok := resp.Result.(*CallToolResult)
	if !ok {
		t.Fatalf("tool %s: expected *CallToolResult, got %T (transport error: %+v)", name, resp.Result, resp.Error)
	}
	return result
}

func TestSendMessageTool_RegistrationGating(t *testing.T) {
	sender := &fakeSender{}
	tests := []struct {
		name           string
		opts           ServerOptions
		wantRegistered bool
	}{
		{
			name:           "disabled flag with sender",
			opts:           ServerOptions{EnableSendMessage: false, Sender: sender},
			wantRegistered: false,
		},
		{
			name:           "enabled flag without sender",
			opts:           ServerOptions{EnableSendMessage: true},
			wantRegistered: false,
		},
		{
			name:           "enabled flag with sender",
			opts:           ServerOptions{EnableSendMessage: true, Sender: sender},
			wantRegistered: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := NewServer(tc.opts)
			names := sendMessageToolNames(t, srv)
			if got := names["send_message"]; got != tc.wantRegistered {
				t.Fatalf("send_message registered=%v, want %v", got, tc.wantRegistered)
			}
		})
	}
}

func TestSendMessage_Text(t *testing.T) {
	sender := &fakeSender{}
	srv := NewServer(ServerOptions{EnableSendMessage: true, Sender: sender})

	text := callTool(t, srv, "send_message", map[string]any{
		"conversation_id":     "conv-text",
		"text":                "hello from the test",
		"reply_to_message_id": "reply-1",
	})
	if !strings.Contains(text, "tmp-test") {
		t.Fatalf("expected tmp id in result, got: %s", text)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.textCalls) != 1 {
		t.Fatalf("expected 1 text send, got %d", len(sender.textCalls))
	}
	call := sender.textCalls[0]
	if call.ConversationID != "conv-text" || call.Text != "hello from the test" || call.ReplyToID != "reply-1" {
		t.Fatalf("unexpected text send args: %+v", call)
	}
}

func TestSendMessage_ReactionPersistence_RealDB(t *testing.T) {
	database := openMCPTestDB(t)
	sender := &fakeSender{}
	srv := NewServer(ServerOptions{
		EnableSendMessage: true,
		Sender:            sender,
		Database:          database,
	})

	ctx := context.Background()
	tag := time.Now().UnixNano()
	convID := fmt.Sprintf("mcp-react-conv-%d", tag)
	seedConversation(t, database, convID, "Reaction Contact", true)

	sentAt := time.Now().UTC().Truncate(time.Microsecond)
	msgID := fmt.Sprintf("mcp-react-msg-%d", tag)
	if err := database.UpsertMessage(ctx, &db.Message{
		ID:             msgID,
		ConversationID: convID,
		SenderID:       "+15550000000",
		SenderName:     "Contact",
		SentAt:         sentAt,
		Body:           "react to me",
	}); err != nil {
		t.Fatalf("seed message: %v", err)
	}

	callTool(t, srv, "send_message", map[string]any{
		"conversation_id":     convID,
		"reaction_message_id": msgID,
		"reaction_emoji":      "👍",
	})

	sender.mu.Lock()
	if len(sender.reactionCalls) != 1 {
		sender.mu.Unlock()
		t.Fatalf("expected 1 reaction send, got %d", len(sender.reactionCalls))
	}
	call := sender.reactionCalls[0]
	sender.mu.Unlock()
	if call.MessageID != msgID || call.Emoji != "👍" || call.Remove {
		t.Fatalf("unexpected reaction send args: %+v", call)
	}

	msgs, err := database.GetMessagesAroundTime(ctx, convID, sentAt, 1)
	if err != nil {
		t.Fatalf("get messages: %v", err)
	}
	var found *db.Message
	for i := range msgs {
		if msgs[i].ID == msgID {
			found = &msgs[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("seeded message %s not found after reaction", msgID)
	}
	if len(found.Reactions) != 1 || found.Reactions[0].Emoji != "👍" || found.Reactions[0].Sender != "me" {
		t.Fatalf("expected persisted reaction me/👍, got %+v", found.Reactions)
	}
}

func TestSendMessage_ReactionRemovePersistence_RealDB(t *testing.T) {
	database := openMCPTestDB(t)
	sender := &fakeSender{}
	srv := NewServer(ServerOptions{
		EnableSendMessage: true,
		Sender:            sender,
		Database:          database,
	})

	ctx := context.Background()
	convID := "mcp-reactrm-conv-" + time.Now().Format("150405.000000000")
	seedConversation(t, database, convID, "Reaction Remove Contact", true)

	sentAt := time.Now().UTC().Truncate(time.Microsecond)
	msgID := "mcp-reactrm-msg-" + time.Now().Format("150405.000000000")
	if err := database.UpsertMessage(ctx, &db.Message{
		ID:             msgID,
		ConversationID: convID,
		SenderID:       "+15550000000",
		SenderName:     "Contact",
		SentAt:         sentAt,
		Body:           "react to me",
		Reactions:      []db.Reaction{{Sender: "me", Emoji: "👍"}},
	}); err != nil {
		t.Fatalf("seed message: %v", err)
	}

	callTool(t, srv, "send_message", map[string]any{
		"conversation_id":     convID,
		"reaction_message_id": msgID,
		"reaction_emoji":      "👍",
		"reaction_remove":     true,
	})

	sender.mu.Lock()
	if len(sender.reactionCalls) != 1 {
		sender.mu.Unlock()
		t.Fatalf("expected 1 reaction send, got %d", len(sender.reactionCalls))
	}
	call := sender.reactionCalls[0]
	sender.mu.Unlock()
	if !call.Remove {
		t.Fatalf("expected remove=true, got %+v", call)
	}

	msgs, err := database.GetMessagesAroundTime(ctx, convID, sentAt, 1)
	if err != nil {
		t.Fatalf("get messages: %v", err)
	}
	var found *db.Message
	for i := range msgs {
		if msgs[i].ID == msgID {
			found = &msgs[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("seeded message %s not found after reaction removal", msgID)
	}
	if len(found.Reactions) != 0 {
		t.Fatalf("expected no reactions after remove, got %+v", found.Reactions)
	}
}

func TestSendMessage_MediaDecode(t *testing.T) {
	sender := &fakeSender{}
	srv := NewServer(ServerOptions{EnableSendMessage: true, Sender: sender})

	payload := []byte("hello media payload")
	encoded := base64.StdEncoding.EncodeToString(payload)

	text := callTool(t, srv, "send_message", map[string]any{
		"conversation_id": "conv-media",
		"media_base64":    encoded,
		"media_file_name": "greeting.txt",
		"media_mime_type": "text/plain",
		"caption":         "a caption",
	})
	if !strings.Contains(text, "media-test") {
		t.Fatalf("expected media id in result, got: %s", text)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.mediaCalls) != 1 {
		t.Fatalf("expected 1 media send, got %d", len(sender.mediaCalls))
	}
	call := sender.mediaCalls[0]
	if string(call.Data) != string(payload) {
		t.Fatalf("decoded payload mismatch: got %q", string(call.Data))
	}
	if call.FileName != "greeting.txt" || call.MimeType != "text/plain" || call.Caption != "a caption" {
		t.Fatalf("unexpected media metadata: %+v", call)
	}
}

func TestSendMessage_MediaValidation(t *testing.T) {
	sender := &fakeSender{}
	srv := NewServer(ServerOptions{EnableSendMessage: true, Sender: sender})

	tests := []struct {
		name string
		args map[string]any
		want string
	}{
		{
			name: "invalid base64",
			args: map[string]any{"conversation_id": "conv", "media_base64": "not base64!!!"},
			want: "not valid base64",
		},
		{
			name: "zero bytes",
			args: map[string]any{"conversation_id": "conv", "media_base64": ""},
			want: "provide text, media_base64, or reaction_message_id",
		},
		{
			name: "missing conversation",
			args: map[string]any{"text": "hello"},
			want: "conversation_id is required",
		},
		{
			name: "mixed modes",
			args: map[string]any{"conversation_id": "conv", "text": "hello", "reaction_message_id": "m1", "reaction_emoji": "👍"},
			want: "provide only one of",
		},
		{
			name: "reaction without emoji",
			args: map[string]any{"conversation_id": "conv", "reaction_message_id": "m1"},
			want: "reaction_emoji is required",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := callToolRaw(t, srv, "send_message", tc.args)
			if !result.IsError {
				t.Fatalf("expected error result for %s, got %+v", tc.name, result)
			}
			if len(result.Content) == 0 || !strings.Contains(result.Content[0].Text, tc.want) {
				t.Fatalf("expected error containing %q, got %+v", tc.want, result.Content)
			}
		})
	}

	// Empty base64 with only whitespace decodes to zero bytes when supplied as a
	// non-empty string. Validate the explicit size/empty guards.
	oversized := base64.StdEncoding.EncodeToString(make([]byte, maxOutboundMediaBytes+1))
	result := callToolRaw(t, srv, "send_message", map[string]any{
		"conversation_id": "conv",
		"media_base64":    oversized,
	})
	if !result.IsError || !strings.Contains(result.Content[0].Text, "exceeds") {
		t.Fatalf("expected oversize media error, got %+v", result.Content)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.textCalls)+len(sender.mediaCalls)+len(sender.reactionCalls) != 0 {
		t.Fatalf("expected no sends for invalid requests, got text=%d media=%d reaction=%d",
			len(sender.textCalls), len(sender.mediaCalls), len(sender.reactionCalls))
	}
}

func TestSendMessage_FailsClosedWithoutSender(t *testing.T) {
	// The tool is not registered without a sender (covered by the registration
	// gating test). This exercises the handler-level guard in case a caller
	// registers send_message manually with a nil transport.
	handler := builtinHandler("send_message", nil, nil, nil, nil)
	_, err := handler(context.Background(), json.RawMessage(`{"conversation_id":"conv","text":"hello"}`))
	if err == nil || !strings.Contains(err.Error(), "no outbound transport") {
		t.Fatalf("expected no-transport error, got %v", err)
	}
}
