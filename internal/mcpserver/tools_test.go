package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestSendMessageTool_RegistrationGuard(t *testing.T) {
	// Guardrail default: Send-Message disabled
	serverDefault := NewServer(ServerOptions{
		EnableSendMessage: false,
	})

	listReq := &JSONRPCMessage{
		JSONRPC: "2.0",
		ID:      1,
		Method:  MethodToolsList,
	}
	resp := serverDefault.HandleMessage(context.Background(), listReq)
	if resp == nil || resp.Error != nil {
		t.Fatalf("expected successful tools/list response, got error: %+v", resp)
	}

	resultMap, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("expected result map, got: %T", resp.Result)
	}
	tools, ok := resultMap["tools"].([]Tool)
	if !ok {
		t.Fatalf("expected []Tool in tools, got: %T", resultMap["tools"])
	}

	for _, tool := range tools {
		if tool.Name == "send_message" {
			t.Fatalf("FAIL-CLOSED VIOLATION: send_message registered when EnableSendMessage=false")
		}
	}

	// Opt-in: Send-Message enabled
	serverOptIn := NewServer(ServerOptions{
		EnableSendMessage: true,
	})
	respOptIn := serverOptIn.HandleMessage(context.Background(), listReq)
	resultMapOptIn := respOptIn.Result.(map[string]any)
	toolsOptIn := resultMapOptIn["tools"].([]Tool)

	found := false
	for _, tool := range toolsOptIn {
		if tool.Name == "send_message" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected send_message tool to be registered when EnableSendMessage=true")
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
	server := NewServer(ServerOptions{})

	// Call list_conversations default stub
	callReq := &JSONRPCMessage{
		JSONRPC: "2.0",
		ID:      10,
		Method:  MethodToolsCall,
		Params:  json.RawMessage(`{"name":"list_conversations","arguments":{"limit":5}}`),
	}

	resp := server.HandleMessage(context.Background(), callReq)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	toolRes, ok := resp.Result.(*CallToolResult)
	if !ok {
		t.Fatalf("expected *CallToolResult, got: %T", resp.Result)
	}

	if len(toolRes.Content) == 0 {
		t.Fatalf("expected at least 1 content item")
	}

	text := toolRes.Content[0].Text
	if !strings.Contains(text, UntrustedDataOpenTag) {
		t.Fatalf("expected output to be wrapped in untrusted data tag, got: %s", text)
	}
}
