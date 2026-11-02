package mcpserver

import "encoding/json"

// ProtocolVersion defines the Model Context Protocol specification version implemented by this server.
const ProtocolVersion = "2026-07-28"

// HeaderProtocolVersion is the HTTP response header mandated by the 2026-07-28 MCP specification.
const HeaderProtocolVersion = "Mcp-Protocol-Version"

// JSON-RPC 2.0 standard error codes.
const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternalError  = -32603
)

// Standard MCP method names.
const (
	MethodInitialize  = "initialize"
	MethodInitialized = "notifications/initialized"
	MethodPing        = "ping"
	MethodToolsList   = "tools/list"
	MethodToolsCall   = "tools/call"
)

// JSONRPCMessage represents a JSON-RPC 2.0 request, response, or notification.
type JSONRPCMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *JSONRPCError   `json:"error,omitempty"`
}

// JSONRPCError represents a standard JSON-RPC 2.0 error object.
type JSONRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Implementation contains metadata about the client or server.
type Implementation struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// ServerCapabilities declares MCP capabilities provided by this server.
type ServerCapabilities struct {
	Tools *ToolsCapability `json:"tools,omitempty"`
}

// ToolsCapability declares tool support.
type ToolsCapability struct {
	ListChanged bool `json:"listChanged"`
}

// InitializeParams represents parameters sent by the client during initialization.
type InitializeParams struct {
	ProtocolVersion string         `json:"protocolVersion"`
	Capabilities    map[string]any `json:"capabilities,omitempty"`
	ClientInfo      Implementation `json:"clientInfo"`
}

// InitializeResult represents the server's response to the initialize request.
type InitializeResult struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    ServerCapabilities `json:"capabilities"`
	ServerInfo      Implementation     `json:"serverInfo"`
}

// Tool represents a registered MCP tool definition.
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

// ToolContent represents a single item in a tool's output.
type ToolContent struct {
	Type string `json:"type"` // "text", "image", "resource"
	Text string `json:"text,omitempty"`
}

// CallToolParams contains arguments for tools/call.
type CallToolParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// CallToolResult represents the output of a tool call.
type CallToolResult struct {
	Content []ToolContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}
