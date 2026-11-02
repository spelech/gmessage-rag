package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/spelech/gmessage-rag/internal/db"
	"github.com/spelech/gmessage-rag/internal/mcpserver"
)

// SearchDatabase defines the storage interface consumed by the test bench.
type SearchDatabase interface {
	SearchSessionsHybrid_RRF(ctx context.Context, conversationID string, queryText string, limit int) ([]db.SessionSearchResult, error)
	SearchMessagesFTS(ctx context.Context, conversationID string, queryText string, limit int) ([]db.Message, error)
}

// TestBenchHandler orchestrates diagnostic endpoints for MCP tools, guardrails, and hybrid RRF search (UI-02).
type TestBenchHandler struct {
	mcpServer *mcpserver.Server
	db        SearchDatabase
}

// NewTestBench is an alias for NewTestBenchHandler.
func NewTestBench(mcpServer *mcpserver.Server, database SearchDatabase) *TestBenchHandler {
	return NewTestBenchHandler(mcpServer, database)
}

// NewTestBenchHandler constructs a new TestBenchHandler.
func NewTestBenchHandler(mcpServer *mcpserver.Server, database SearchDatabase) *TestBenchHandler {
	return &TestBenchHandler{
		mcpServer: mcpServer,
		db:        database,
	}
}

// RegisterRoutes registers test bench endpoints on the provided http.ServeMux.
func (h *TestBenchHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/v1/testbench/mcp", h.HandleMCP)
	mux.HandleFunc("/api/v1/testbench/guardrail", h.HandleGuardrail)
	mux.HandleFunc("/api/v1/testbench/search", h.HandleSearch)
}

// MCPTestRequest represents the incoming payload for POST /api/v1/testbench/mcp.
type MCPTestRequest struct {
	Tool      string          `json:"tool"`
	Arguments json.RawMessage `json:"arguments"`
}

// GuardrailTestRequest represents the incoming payload for POST /api/v1/testbench/guardrail.
type GuardrailTestRequest struct {
	Text string `json:"text"`
}

// GuardrailTestResponse represents the response payload for POST /api/v1/testbench/guardrail.
type GuardrailTestResponse struct {
	Wrapped string `json:"wrapped"`
}

// SearchTestRequest represents the incoming payload for POST /api/v1/testbench/search.
type SearchTestRequest struct {
	Query          string `json:"query"`
	ConversationID string `json:"conversation_id"`
	Limit          int    `json:"limit"`
	Mode           string `json:"mode"` // "hybrid" (default) or "fts"
}

// SearchTestResponse represents the response payload for POST /api/v1/testbench/search.
type SearchTestResponse struct {
	Mode    string `json:"mode"`
	Count   int    `json:"count"`
	Results any    `json:"results"`
}

// HandleMCP executes an MCP tool via the MCP server and returns the result payload.
func (h *TestBenchHandler) HandleMCP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"Method Not Allowed; POST required"}`, http.StatusMethodNotAllowed)
		return
	}

	var req MCPTestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("invalid json payload: %v", err)})
		return
	}

	if req.Tool == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "tool name is required"})
		return
	}

	if h.mcpServer == nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "mcp server not configured"})
		return
	}

	args := req.Arguments
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}

	callParams := mcpserver.CallToolParams{
		Name:      req.Tool,
		Arguments: args,
	}
	rawParams, err := json.Marshal(callParams)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("failed to marshal params: %v", err)})
		return
	}

	jsonRPCMsg := &mcpserver.JSONRPCMessage{
		JSONRPC: "2.0",
		ID:      "testbench-mcp-call",
		Method:  mcpserver.MethodToolsCall,
		Params:  rawParams,
	}

	resp := h.mcpServer.HandleMessage(r.Context(), jsonRPCMsg)
	if resp == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if resp.Error != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(resp)
		return
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp.Result)
}

// HandleGuardrail runs WrapUntrustedData on the input text and returns the escaped XML boundary string.
func (h *TestBenchHandler) HandleGuardrail(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"Method Not Allowed; POST required"}`, http.StatusMethodNotAllowed)
		return
	}

	var req GuardrailTestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("invalid json payload: %v", err)})
		return
	}

	wrapped := mcpserver.WrapUntrustedData(req.Text)
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(GuardrailTestResponse{
		Wrapped: wrapped,
	})
}

// HandleSearch dispatches search queries to either db.SearchSessionsHybrid_RRF or db.SearchMessagesFTS.
func (h *TestBenchHandler) HandleSearch(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"Method Not Allowed; POST required"}`, http.StatusMethodNotAllowed)
		return
	}

	if h.db == nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "database not configured"})
		return
	}

	var req SearchTestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("invalid json payload: %v", err)})
		return
	}

	if strings.TrimSpace(req.Query) == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "query string is required"})
		return
	}

	limit := req.Limit
	if limit <= 0 {
		limit = 10
	}

	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	if mode == "" || mode == "hybrid" || mode == "rrf" {
		sessions, err := h.db.SearchSessionsHybrid_RRF(r.Context(), req.ConversationID, req.Query, limit)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("hybrid search error: %v", err)})
			return
		}
		if sessions == nil {
			sessions = []db.SessionSearchResult{}
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(SearchTestResponse{
			Mode:    "hybrid",
			Count:   len(sessions),
			Results: sessions,
		})
		return
	}

	if mode == "fts" || mode == "messages" {
		messages, err := h.db.SearchMessagesFTS(r.Context(), req.ConversationID, req.Query, limit)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("fts search error: %v", err)})
			return
		}
		if messages == nil {
			messages = []db.Message{}
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(SearchTestResponse{
			Mode:    "fts",
			Count:   len(messages),
			Results: messages,
		})
		return
	}

	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error": fmt.Sprintf("unsupported search mode %q (must be 'hybrid' or 'fts')", req.Mode),
	})
}
