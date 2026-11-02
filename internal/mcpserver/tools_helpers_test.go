package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/spelech/gmessage-rag/internal/db"
)

// openMCPTestDB connects to the real Postgres instance named by DATABASE_URL.
// Tests skip cleanly when the URL is unset or the database is unreachable.
func openMCPTestDB(t *testing.T) *db.DB {
	t.Helper()

	connStr := os.Getenv("DATABASE_URL")
	if connStr == "" {
		t.Skip("DATABASE_URL not set; skipping real-DB MCP integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	database, err := db.New(ctx, connStr)
	if err != nil {
		t.Skipf("skipping real-DB MCP integration test: database unreachable: %v", err)
	}

	if err := database.Migrate(ctx); err != nil {
		database.Close()
		t.Fatalf("database migration failed: %v", err)
	}

	t.Cleanup(database.Close)
	return database
}

// callTool dispatches tools/call through the real JSON-RPC surface and returns the sanitized text.
func callTool(t *testing.T, srv *Server, name string, args any) string {
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
		ID:      "integration",
		Method:  MethodToolsCall,
		Params:  params,
	})
	if resp.Error != nil {
		t.Fatalf("tool %s returned transport error: %+v", name, resp.Error)
	}

	result, ok := resp.Result.(*CallToolResult)
	if !ok {
		t.Fatalf("tool %s: expected *CallToolResult, got %T", name, resp.Result)
	}
	if result.IsError {
		t.Fatalf("tool %s returned an error result: %s", name, result.Content[0].Text)
	}
	if len(result.Content) == 0 {
		t.Fatalf("tool %s returned no content", name)
	}

	text := result.Content[0].Text
	if !strings.Contains(text, UntrustedDataOpenTag) || !strings.Contains(text, UntrustedDataCloseTag) {
		t.Fatalf("tool %s output is not wrapped in untrusted-data boundaries: %s", name, text)
	}
	return text
}

func seedConversation(t *testing.T, database *db.DB, id, name string, tracked bool) *db.Conversation {
	t.Helper()
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Microsecond)
	conv := &db.Conversation{
		ID:            id,
		Name:          name,
		Participants:  []string{"+15550000000"},
		IsTracked:     tracked,
		LastMessageAt: &now,
		Metadata:      map[string]any{"source": "mcp-integration-test"},
	}
	if err := database.UpsertConversation(ctx, conv); err != nil {
		t.Fatalf("seed conversation %s: %v", id, err)
	}
	return conv
}
