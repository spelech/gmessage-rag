package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/spelech/gmessage-rag/internal/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSendMessageTool_RegistrationGuard(t *testing.T) {
	// Guardrail: send_message is never registered regardless of the
	// EnableSendMessage flag, because outbound transport wiring is not
	// yet implemented.

	for _, enable := range []bool{false, true} {
		server := NewServer(ServerOptions{EnableSendMessage: enable})
		resp := server.HandleMessage(context.Background(), &JSONRPCMessage{
			JSONRPC: "2.0", ID: 1, Method: MethodToolsList,
		})
		if resp == nil || resp.Error != nil {
			t.Fatalf("expected successful tools/list for EnableSendMessage=%v, got: %+v", enable, resp)
		}
		resultMap, ok := resp.Result.(map[string]any)
		if !ok {
			t.Fatalf("expected result map for EnableSendMessage=%v, got: %T", enable, resp.Result)
		}
		tools, ok := resultMap["tools"].([]Tool)
		if !ok {
			t.Fatalf("expected []Tool for EnableSendMessage=%v, got: %T", enable, resultMap["tools"])
		}
		for _, tool := range tools {
			if tool.Name == "send_message" {
				t.Fatalf("send_message must NOT be registered (EnableSendMessage=%v): outbound transport not wired", enable)
			}
		}
	}
}

func TestTool_ListConversations(t *testing.T) {
	server := NewServer(ServerOptions{})

	listReq := &JSONRPCMessage{
		JSONRPC: "2.0",
		ID:      1,
		Method:  MethodToolsList,
	}
	resp := server.HandleMessage(context.Background(), listReq)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	resultMap := resp.Result.(map[string]any)
	tools := resultMap["tools"].([]Tool)

	expectedTools := map[string]bool{
		"list_conversations":          false,
		"get_contact_dossier":         false,
		"search_conversation_history": false,
		"get_messages_around_time":    false,
		"search_shared_media":         false,
		"manage_tracked_contact":      false,
	}

	for _, tool := range tools {
		if _, ok := expectedTools[tool.Name]; ok {
			expectedTools[tool.Name] = true
		}
	}

	for name, found := range expectedTools {
		if !found {
			t.Errorf("expected tool %q was not found in registered tools", name)
		}
	}
}

func TestTool_CallExecution(t *testing.T) {
	t.Parallel()

	server := NewServer(ServerOptions{})

	// Calling a tool without a configured database fails closed with an error result.
	callReq := &JSONRPCMessage{
		JSONRPC: "2.0",
		ID:      10,
		Method:  MethodToolsCall,
		Params:  json.RawMessage(`{"name":"list_conversations","arguments":{"limit":5}}`),
	}

	resp := server.HandleMessage(context.Background(), callReq)
	if resp.Error != nil {
		t.Fatalf("unexpected transport error: %+v", resp.Error)
	}

	toolRes, ok := resp.Result.(*CallToolResult)
	if !ok {
		t.Fatalf("expected *CallToolResult, got: %T", resp.Result)
	}

	if !toolRes.IsError {
		t.Fatalf("expected fail-closed error result when no database is configured, got: %+v", toolRes)
	}
	if len(toolRes.Content) == 0 {
		t.Fatalf("expected at least 1 content item")
	}
	if !strings.Contains(toolRes.Content[0].Text, "database is not configured") {
		t.Fatalf("expected database configuration error, got: %s", toolRes.Content[0].Text)
	}
}

// TestServer_AccessTokenEnforcement verifies MCP_ACCESS_TOKEN gating on tools/call.
func TestServer_AccessTokenEnforcement(t *testing.T) {
	server := NewServer(ServerOptions{AccessToken: "s3cret"})

	params, err := json.Marshal(CallToolParams{
		Name:      "list_conversations",
		Arguments: json.RawMessage(`{"limit":1}`),
	})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	call := &JSONRPCMessage{JSONRPC: "2.0", ID: 1, Method: MethodToolsCall, Params: params}

	// Missing token is rejected.
	resp := server.HandleMessage(context.Background(), call)
	if resp.Error == nil || resp.Error.Code != CodeInvalidRequest {
		t.Fatalf("expected unauthorized error for missing token, got: %+v", resp)
	}

	// Wrong token is rejected.
	ctxWrong := WithAccessToken(context.Background(), "wrong")
	if resp := server.HandleMessage(ctxWrong, call); resp.Error == nil {
		t.Fatalf("expected unauthorized error for wrong token, got: %+v", resp)
	}

	// Correct token passes the access gate (handler then fails closed on the nil DB).
	ctxOK := WithAccessToken(context.Background(), "s3cret")
	respOK := server.HandleMessage(ctxOK, call)
	if respOK.Error != nil {
		t.Fatalf("expected authorized call to reach the handler, got transport error: %+v", respOK.Error)
	}
	if res, ok := respOK.Result.(*CallToolResult); !ok || !res.IsError {
		t.Fatalf("expected authorized handler to fail closed on nil database, got: %+v", respOK.Result)
	}
}

// [Requirement(MCP-01)]
func TestTools_ListConversations_RealDB(t *testing.T) {
	database := openMCPTestDB(t)
	server := NewServer(ServerOptions{Database: database})

	tag := time.Now().UnixNano()
	convID := fmt.Sprintf("mcp-list-conv-%d", tag)
	convName := fmt.Sprintf("MCP List Contact %d", tag)
	seedConversation(t, database, convID, convName, true)

	text := callTool(t, server, "list_conversations", map[string]any{
		"limit":        50,
		"tracked_only": true,
	})

	if !strings.Contains(text, convID) {
		t.Fatalf("expected seeded conversation ID %q in output, got: %s", convID, text)
	}
	if !strings.Contains(text, convName) {
		t.Fatalf("expected seeded conversation name %q in output, got: %s", convName, text)
	}
}

// [Requirement(MCP-02)]
func TestTools_GetContactDossier_RealDB(t *testing.T) {
	database := openMCPTestDB(t)
	server := NewServer(ServerOptions{Database: database})

	tag := time.Now().UnixNano()
	convID := fmt.Sprintf("mcp-dossier-conv-%d", tag)
	seedConversation(t, database, convID, fmt.Sprintf("Dossier Contact %d", tag), true)

	ctx := context.Background()
	base := time.Now().UTC().Truncate(time.Microsecond).Add(-2 * time.Hour)
	markdown := fmt.Sprintf("## Living dossier %d\n- Open loop: confirm lunch", tag)
	if _, err := database.UpsertContactDossier(
		ctx,
		convID,
		fmt.Sprintf("Dossier Contact %d", tag),
		markdown,
		map[string]any{"open_loops": []string{"confirm lunch"}},
		nil,
		&base,
	); err != nil {
		t.Fatalf("seed dossier: %v", err)
	}

	tailBody := fmt.Sprintf("unindexed live tail %d", tag)
	msgID := fmt.Sprintf("mcp-dossier-msg-%d", tag)
	if err := database.UpsertMessage(ctx, &db.Message{
		ID:             msgID,
		ConversationID: convID,
		SenderID:       "+15550000000",
		SenderName:     "Contact",
		SentAt:         base.Add(10 * time.Minute),
		Body:           tailBody,
	}); err != nil {
		t.Fatalf("seed live-tail message: %v", err)
	}

	text := callTool(t, server, "get_contact_dossier", map[string]any{
		"conversation_id": convID,
	})

	if !strings.Contains(text, fmt.Sprintf("Living dossier %d", tag)) {
		t.Fatalf("expected dossier markdown in output, got: %s", text)
	}
	if !strings.Contains(text, "confirm lunch") {
		t.Fatalf("expected structured open loop in output, got: %s", text)
	}
	if !strings.Contains(text, tailBody) {
		t.Fatalf("expected live-tail message %q in output, got: %s", tailBody, text)
	}
}

// [Requirement(MCP-03)]
func TestTools_SearchConversationHistory_RealDB(t *testing.T) {
	database := openMCPTestDB(t)
	server := NewServer(ServerOptions{Database: database})

	tag := time.Now().UnixNano()
	convID := fmt.Sprintf("mcp-search-conv-%d", tag)
	seedConversation(t, database, convID, fmt.Sprintf("Search Contact %d", tag), true)

	keyword := fmt.Sprintf("zephyrkeyword%d", tag)
	started := time.Now().UTC().Truncate(time.Microsecond).Add(-90 * time.Minute)
	transcript := fmt.Sprintf("Alice: let's review the %s rollout plan.", keyword)
	if _, err := database.UpsertSession(context.Background(), &db.ConversationSession{
		ConversationID: convID,
		StartedAt:      started,
		EndedAt:        started.Add(45 * time.Minute),
		MessageCount:   3,
		Transcript:     transcript,
		IsProcessed:    true,
	}, nil); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	text := callTool(t, server, "search_conversation_history", map[string]any{
		"query":           keyword,
		"conversation_id": convID,
		"limit":           5,
	})

	if !strings.Contains(text, keyword) {
		t.Fatalf("expected matched transcript containing %q, got: %s", keyword, text)
	}

	// Omitting conversation_id searches across every conversation.
	crossText := callTool(t, server, "search_conversation_history", map[string]any{
		"query": keyword,
		"limit": 5,
	})
	if !strings.Contains(crossText, keyword) {
		t.Fatalf("expected cross-conversation search to match %q, got: %s", keyword, crossText)
	}
}

// [Requirement(MCP-04)]
func TestTools_GetMessagesAroundTime_RealDB(t *testing.T) {
	database := openMCPTestDB(t)
	server := NewServer(ServerOptions{Database: database})

	tag := time.Now().UnixNano()
	convID := fmt.Sprintf("mcp-window-conv-%d", tag)
	seedConversation(t, database, convID, fmt.Sprintf("Window Contact %d", tag), true)

	ctx := context.Background()
	target := time.Now().UTC().Truncate(time.Microsecond)
	body := fmt.Sprintf("verbatim window message %d", tag)
	msgID := fmt.Sprintf("mcp-window-msg-%d", tag)
	if err := database.UpsertMessage(ctx, &db.Message{
		ID:             msgID,
		ConversationID: convID,
		SenderID:       "+15550000000",
		SenderName:     "Contact",
		SentAt:         target.Add(5 * time.Minute),
		Body:           body,
	}); err != nil {
		t.Fatalf("seed window message: %v", err)
	}

	ocr := fmt.Sprintf("OCR annotation %d", tag)
	if err := database.UpsertMediaAttachment(ctx, &db.MediaAttachment{
		MessageID:      msgID,
		ConversationID: convID,
		SHA256Hash:     fmt.Sprintf("hash-%d", tag),
		RelativePath:   fmt.Sprintf("aa/bb/hash-%d.jpg", tag),
		MimeType:       "image/jpeg",
		FileSizeBytes:  1024,
		OCRText:        &ocr,
	}); err != nil {
		t.Fatalf("seed media attachment: %v", err)
	}

	text := callTool(t, server, "get_messages_around_time", map[string]any{
		"conversation_id": convID,
		"timestamp":       target.Format(time.RFC3339),
		"window_minutes":  30,
	})

	if !strings.Contains(text, body) {
		t.Fatalf("expected verbatim message body in output, got: %s", text)
	}
	if !strings.Contains(text, ocr) {
		t.Fatalf("expected media OCR annotation %q in output, got: %s", ocr, text)
	}
}

// [Requirement(MCP-05)]
func TestTools_SearchSharedMedia_RealDB(t *testing.T) {
	database := openMCPTestDB(t)
	server := NewServer(ServerOptions{Database: database})

	ctx := context.Background()
	tag := time.Now().UnixNano()
	convID := fmt.Sprintf("mcp-media-conv-%d", tag)
	seedConversation(t, database, convID, fmt.Sprintf("Media Contact %d", tag), true)

	msgID := fmt.Sprintf("mcp-media-msg-%d", tag)
	if err := database.UpsertMessage(ctx, &db.Message{
		ID:             msgID,
		ConversationID: convID,
		SenderID:       "+15550000000",
		SenderName:     "Contact",
		SentAt:         time.Now().UTC().Truncate(time.Microsecond),
		Body:           "shared an image",
	}); err != nil {
		t.Fatalf("seed media message: %v", err)
	}

	ocr := fmt.Sprintf("concertticket%d", tag)
	if err := database.UpsertMediaAttachment(ctx, &db.MediaAttachment{
		MessageID:      msgID,
		ConversationID: convID,
		SHA256Hash:     fmt.Sprintf("media-hash-%d", tag),
		RelativePath:   fmt.Sprintf("cc/dd/media-%d.png", tag),
		MimeType:       "image/png",
		FileSizeBytes:  2048,
		OCRText:        &ocr,
	}); err != nil {
		t.Fatalf("seed searchable media: %v", err)
	}

	text := callTool(t, server, "search_shared_media", map[string]any{
		"query":           ocr,
		"conversation_id": convID,
		"limit":           10,
	})

	if !strings.Contains(text, ocr) {
		t.Fatalf("expected media attachment OCR %q in output, got: %s", ocr, text)
	}
}

// [Requirement(MCP-06)]
func TestTools_ManageTrackedContact_RealDB(t *testing.T) {
	database := openMCPTestDB(t)
	server := NewServer(ServerOptions{Database: database})

	ctx := context.Background()
	tag := time.Now().UnixNano()
	convID := fmt.Sprintf("mcp-manage-conv-%d", tag)
	seedConversation(t, database, convID, fmt.Sprintf("Manage Contact %d", tag), false)

	// 1. track flips is_tracked in the database.
	callTool(t, server, "manage_tracked_contact", map[string]any{
		"conversation_id": convID,
		"action":          "track",
	})
	tracked, err := database.GetConversation(ctx, convID)
	if err != nil {
		t.Fatalf("get conversation after track: %v", err)
	}
	if !tracked.IsTracked {
		t.Fatalf("expected is_tracked=true after track, got false")
	}

	// 2. untrack flips is_tracked back.
	callTool(t, server, "manage_tracked_contact", map[string]any{
		"conversation_id": convID,
		"action":          "untrack",
	})
	untracked, err := database.GetConversation(ctx, convID)
	if err != nil {
		t.Fatalf("get conversation after untrack: %v", err)
	}
	if untracked.IsTracked {
		t.Fatalf("expected is_tracked=false after untrack, got true")
	}

	// 3. backfill resets the persisted backfill cursor and completion flag.
	cursor := "cursor-123"
	completed := *untracked
	completed.BackfillCursor = &cursor
	completed.BackfillComplete = true
	if err := database.UpsertConversation(ctx, &completed); err != nil {
		t.Fatalf("seed backfill state: %v", err)
	}

	callTool(t, server, "manage_tracked_contact", map[string]any{
		"conversation_id": convID,
		"action":          "backfill",
	})
	reset, err := database.GetConversation(ctx, convID)
	if err != nil {
		t.Fatalf("get conversation after backfill: %v", err)
	}
	if reset.BackfillCursor != nil {
		t.Fatalf("expected nil backfill cursor after backfill, got %v", *reset.BackfillCursor)
	}
	if reset.BackfillComplete {
		t.Fatalf("expected backfill_complete=false after backfill, got true")
	}
}

func TestManageTrackedContact_UnitValidation(t *testing.T) {
	srv := NewServer(ServerOptions{
		Name:     "test",
		Database: nil,
	})

	// With nil DB, should return database not configured error
	resp := srv.HandleMessage(context.Background(), &JSONRPCMessage{
		JSONRPC: "2.0",
		ID:      "test-1",
		Method:  MethodToolsCall,
		Params:  []byte(`{"name":"manage_tracked_contact","arguments":{"conversation_id":"c1","action":"track"}}`),
	})
	require.NotNil(t, resp)
	res, ok := resp.Result.(*CallToolResult)
	require.True(t, ok)
	assert.True(t, res.IsError)
}

func TestParseOptionalTime(t *testing.T) {
	pt, err := parseOptionalTime("")
	assert.NoError(t, err)
	assert.Nil(t, pt)

	pt2, err := parseOptionalTime("2026-11-03T15:00:00Z")
	assert.NoError(t, err)
	assert.NotNil(t, pt2)

	_, err = parseOptionalTime("invalid-time")
	assert.Error(t, err)
}
