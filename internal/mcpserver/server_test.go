package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProtocol_20260728_PingAndHandshake(t *testing.T) {
	server := NewServer(ServerOptions{
		Name:    "gmessages-rag",
		Version: "0.1.0",
	})

	ctx := context.Background()

	// Invariant 1: ping method returns empty object {} with matching request ID
	pingReq := &JSONRPCMessage{
		JSONRPC: "2.0",
		ID:      "ping-12345",
		Method:  MethodPing,
	}

	pingResp := server.HandleMessage(ctx, pingReq)
	if pingResp == nil {
		t.Fatalf("expected ping response, got nil")
	}
	if pingResp.Error != nil {
		t.Fatalf("unexpected error in ping response: %+v", pingResp.Error)
	}
	if pingResp.ID != "ping-12345" {
		t.Fatalf("expected response ID 'ping-12345', got: %v", pingResp.ID)
	}
	resMap, ok := pingResp.Result.(map[string]any)
	if !ok || len(resMap) != 0 {
		t.Fatalf("expected empty object {} for ping result, got: %+v", pingResp.Result)
	}

	// Invariant 2: initialize method returns 2026-07-28 protocolVersion
	initReq := &JSONRPCMessage{
		JSONRPC: "2.0",
		ID:      1,
		Method:  MethodInitialize,
		Params:  json.RawMessage(`{"protocolVersion":"2026-07-28","clientInfo":{"name":"test-client","version":"1.0.0"}}`),
	}

	initResp := server.HandleMessage(ctx, initReq)
	if initResp == nil || initResp.Error != nil {
		t.Fatalf("initialize failed: %+v", initResp)
	}
	initResult, ok := initResp.Result.(InitializeResult)
	if !ok {
		t.Fatalf("expected InitializeResult, got: %T", initResp.Result)
	}
	if initResult.ProtocolVersion != "2026-07-28" {
		t.Fatalf("expected ProtocolVersion '2026-07-28', got: %s", initResult.ProtocolVersion)
	}
	if initResult.ServerInfo.Name != "gmessages-rag" {
		t.Fatalf("expected server name 'gmessages-rag', got: %s", initResult.ServerInfo.Name)
	}

	// Invariant 3: notifications/initialized returns nil (no response)
	notif := &JSONRPCMessage{
		JSONRPC: "2.0",
		Method:  MethodInitialized,
	}
	notifResp := server.HandleMessage(ctx, notif)
	if notifResp != nil {
		t.Fatalf("expected nil response for notification, got: %+v", notifResp)
	}
}

func TestServer_StreamableHTTP_HeadersAndPing(t *testing.T) {
	server := NewServer(ServerOptions{})

	// Test POST /mcp with ping payload
	reqBody := `{"jsonrpc":"2.0","id":99,"method":"ping"}`
	httpReq := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewBufferString(reqBody))
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, httpReq)

	res := rec.Result()
	defer res.Body.Close()

	// Invariant 1: Mcp-Protocol-Version must strictly be 2026-07-28
	protoHeader := res.Header.Get(HeaderProtocolVersion)
	if protoHeader != ProtocolVersion {
		t.Fatalf("expected %s: %s, got: %s", HeaderProtocolVersion, ProtocolVersion, protoHeader)
	}

	// Invariant 2: Status OK
	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got: %d", res.StatusCode)
	}

	var rpcResp JSONRPCMessage
	if err := json.NewDecoder(res.Body).Decode(&rpcResp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if rpcResp.Error != nil {
		t.Fatalf("unexpected RPC error: %+v", rpcResp.Error)
	}
	if rpcResp.ID != float64(99) { // json.Unmarshal unmarshals numbers to float64 by default
		t.Fatalf("expected ID 99, got: %v", rpcResp.ID)
	}
}

func TestServer_StreamableHTTP_Notification(t *testing.T) {
	server := NewServer(ServerOptions{})

	notifBody := `{"jsonrpc":"2.0","method":"notifications/initialized"}`
	httpReq := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewBufferString(notifBody))
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, httpReq)

	res := rec.Result()
	defer res.Body.Close()

	if res.Header.Get(HeaderProtocolVersion) != ProtocolVersion {
		t.Fatalf("expected protocol version header on notification")
	}

	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("expected status 204 No Content for notification, got: %d", res.StatusCode)
	}
}

func TestServer_StreamableHTTP_MethodNotAllowed(t *testing.T) {
	server := NewServer(ServerOptions{})

	httpReq := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, httpReq)
	res := rec.Result()
	defer res.Body.Close()

	if res.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("expected status 405 Method Not Allowed, got: %d", res.StatusCode)
	}
	if res.Header.Get(HeaderProtocolVersion) != ProtocolVersion {
		t.Fatalf("expected protocol header even on Method Not Allowed")
	}
}

func TestServer_SSE_Endpoint(t *testing.T) {
	server := NewServer(ServerOptions{})

	httpReq := httptest.NewRequest(http.MethodGet, "/sse", nil)
	rec := httptest.NewRecorder()

	server.ServeSSE(rec, httpReq)
	res := rec.Result()
	defer res.Body.Close()

	if res.Header.Get(HeaderProtocolVersion) != ProtocolVersion {
		t.Fatalf("expected %s: %s on SSE endpoint", HeaderProtocolVersion, ProtocolVersion)
	}
	if res.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("expected Content-Type text/event-stream, got: %s", res.Header.Get("Content-Type"))
	}
}

func TestServer_StdioTransport(t *testing.T) {
	server := NewServer(ServerOptions{})

	inBuf := bytes.NewBufferString("{\"jsonrpc\":\"2.0\",\"id\":100,\"method\":\"ping\"}\n")
	var outBuf bytes.Buffer

	ctx := context.Background()
	err := server.ServeStdio(ctx, inBuf, &outBuf)
	if err != nil && err != io.EOF {
		t.Fatalf("unexpected stdio error: %v", err)
	}

	var rpcResp JSONRPCMessage
	if err := json.Unmarshal(outBuf.Bytes(), &rpcResp); err != nil {
		t.Fatalf("failed to decode stdio response: %v; raw: %s", err, outBuf.String())
	}

	if rpcResp.ID != float64(100) {
		t.Fatalf("expected ID 100, got: %v", rpcResp.ID)
	}
}

func TestServer_InvalidJSONRPC(t *testing.T) {
	server := NewServer(ServerOptions{})

	// Missing jsonrpc "2.0"
	invalidReq := &JSONRPCMessage{
		JSONRPC: "1.0",
		ID:      1,
		Method:  MethodPing,
	}

	resp := server.HandleMessage(context.Background(), invalidReq)
	if resp.Error == nil || resp.Error.Code != CodeInvalidRequest {
		t.Fatalf("expected CodeInvalidRequest, got: %+v", resp)
	}

	// Unknown method
	unknownReq := &JSONRPCMessage{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "unknown/method",
	}
	respUnknown := server.HandleMessage(context.Background(), unknownReq)
	if respUnknown.Error == nil || respUnknown.Error.Code != CodeMethodNotFound {
		t.Fatalf("expected CodeMethodNotFound, got: %+v", respUnknown)
	}
}
