package mcpserver

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
)

// ServerOptions configures the MCP server instance.
type ServerOptions struct {
	Name              string
	Version           string
	EnableSendMessage bool
}

// Server coordinates JSON-RPC 2.0 handling under the 2026-07-28 MCP specification.
type Server struct {
	mu                sync.RWMutex
	serverInfo        Implementation
	tools             map[string]registeredTool
	enableSendMessage bool
}

// NewServer initializes an MCP server with core tool definitions and 2026-07-28 protocol compliance.
func NewServer(opts ServerOptions) *Server {
	if opts.Name == "" {
		opts.Name = "gmessages-rag"
	}
	if opts.Version == "" {
		opts.Version = "0.1.0"
	}

	s := &Server{
		serverInfo: Implementation{
			Name:    opts.Name,
			Version: opts.Version,
		},
		tools:             make(map[string]registeredTool),
		enableSendMessage: opts.EnableSendMessage,
	}

	// Register core tools with default handlers
	for _, tool := range BuiltinToolDefinitions(opts.EnableSendMessage) {
		s.RegisterTool(tool, DefaultStubHandler(tool.Name))
	}

	return s
}

// RegisterTool registers or overrides a tool and its handler.
func (s *Server) RegisterTool(tool Tool, handler ToolHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tools[tool.Name] = registeredTool{
		tool:    tool,
		handler: handler,
	}
}

// HandleMessage processes a single JSON-RPC 2.0 message according to the 2026-07-28 MCP specification.
func (s *Server) HandleMessage(ctx context.Context, msg *JSONRPCMessage) *JSONRPCMessage {
	if msg.JSONRPC != "2.0" {
		return &JSONRPCMessage{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Error: &JSONRPCError{
				Code:    CodeInvalidRequest,
				Message: "Invalid JSON-RPC protocol version; expected 2.0",
			},
		}
	}

	// Notification handler (no response required)
	if msg.ID == nil {
		if msg.Method == MethodInitialized {
			// Client acknowledged initialization.
			return nil
		}
		// Other notifications can be ignored or logged
		return nil
	}

	switch msg.Method {
	case MethodPing:
		// 2026-07-28 spec: ping must return an empty object {}
		return &JSONRPCMessage{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Result:  map[string]any{},
		}

	case MethodInitialize:
		return &JSONRPCMessage{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Result: InitializeResult{
				ProtocolVersion: ProtocolVersion,
				Capabilities: ServerCapabilities{
					Tools: &ToolsCapability{ListChanged: false},
				},
				ServerInfo: s.serverInfo,
			},
		}

	case MethodToolsList:
		s.mu.RLock()
		toolList := make([]Tool, 0, len(s.tools))
		for _, rt := range s.tools {
			toolList = append(toolList, rt.tool)
		}
		s.mu.RUnlock()

		return &JSONRPCMessage{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Result: map[string]any{
				"tools": toolList,
			},
		}

	case MethodToolsCall:
		var params CallToolParams
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			return &JSONRPCMessage{
				JSONRPC: "2.0",
				ID:      msg.ID,
				Error: &JSONRPCError{
					Code:    CodeInvalidParams,
					Message: fmt.Sprintf("Failed to parse call params: %v", err),
				},
			}
		}

		s.mu.RLock()
		rt, exists := s.tools[params.Name]
		s.mu.RUnlock()

		if !exists {
			return &JSONRPCMessage{
				JSONRPC: "2.0",
				ID:      msg.ID,
				Error: &JSONRPCError{
					Code:    CodeMethodNotFound,
					Message: fmt.Sprintf("Tool not found: %s", params.Name),
				},
			}
		}

		result, err := rt.handler(ctx, params.Arguments)
		if err != nil {
			return &JSONRPCMessage{
				JSONRPC: "2.0",
				ID:      msg.ID,
				Result: CallToolResult{
					Content: []ToolContent{
						{
							Type: "text",
							Text: fmt.Sprintf("Tool execution error: %v", err),
						},
					},
					IsError: true,
				},
			}
		}

		return &JSONRPCMessage{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Result:  result,
		}

	default:
		return &JSONRPCMessage{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Error: &JSONRPCError{
				Code:    CodeMethodNotFound,
				Message: fmt.Sprintf("Method not found: %s", msg.Method),
			},
		}
	}
}

// ServeHTTP provides the Streamable HTTP transport (POST /mcp).
// It attaches the required Mcp-Protocol-Version: 2026-07-28 response header.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Protocol invariant: attach 2026-07-28 version header to all responses
	w.Header().Set(HeaderProtocolVersion, ProtocolVersion)

	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed; MCP Streamable HTTP requires POST", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(JSONRPCMessage{
			JSONRPC: "2.0",
			Error: &JSONRPCError{
				Code:    CodeParseError,
				Message: "Failed to read request body",
			},
		})
		return
	}

	var req JSONRPCMessage
	if err := json.Unmarshal(body, &req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(JSONRPCMessage{
			JSONRPC: "2.0",
			Error: &JSONRPCError{
				Code:    CodeParseError,
				Message: "Invalid JSON payload",
			},
		})
		return
	}

	resp := s.HandleMessage(r.Context(), &req)
	if resp == nil {
		// Notification acknowledged with 204 No Content
		w.WriteHeader(http.StatusNoContent)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// ServeSSE handles the Server-Sent Events transport (GET /sse).
func (s *Server) ServeSSE(w http.ResponseWriter, r *http.Request) {
	w.Header().Set(HeaderProtocolVersion, ProtocolVersion)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	// Send endpoint event
	fmt.Fprintf(w, "event: endpoint\ndata: /mcp\n\n")
	flusher.Flush()
}

// ServeStdio executes a line-delimited JSON-RPC loop over standard input and output.
func (s *Server) ServeStdio(ctx context.Context, r io.Reader, w io.Writer) error {
	scanner := bufio.NewScanner(r)
	encoder := json.NewEncoder(w)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var req JSONRPCMessage
		if err := json.Unmarshal(line, &req); err != nil {
			errResp := JSONRPCMessage{
				JSONRPC: "2.0",
				Error: &JSONRPCError{
					Code:    CodeParseError,
					Message: "Parse error",
				},
			}
			if encErr := encoder.Encode(errResp); encErr != nil {
				return encErr
			}
			continue
		}

		resp := s.HandleMessage(ctx, &req)
		if resp != nil {
			if err := encoder.Encode(resp); err != nil {
				return err
			}
		}
	}

	return scanner.Err()
}
