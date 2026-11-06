package mcpserver

import (
	"context"
	"encoding/json"
)

// ToolHandler defines the execution function signature for an MCP tool.
type ToolHandler func(ctx context.Context, args json.RawMessage) (*CallToolResult, error)

type registeredTool struct {
	tool    Tool
	handler ToolHandler
}

// BuiltinToolDefinitions returns definitions for the 6 core MCP tools plus optional send_message.
func BuiltinToolDefinitions(enableSendMessage bool) []Tool {
	tools := []Tool{
		{
			Name:        "list_conversations",
			Description: "Lists conversations with IDs, contact names, participant identifiers, tracked status, and last activity timestamp.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"limit": map[string]any{
						"type":        "integer",
						"description": "Maximum number of conversations to return (default 50).",
					},
					"tracked_only": map[string]any{
						"type":        "boolean",
						"description": "If true, only return tracked conversations.",
					},
				},
			},
		},
		{
			Name:        "get_contact_dossier",
			Description: "Returns current Markdown dossier, structured JSON state, plus live tail of unindexed messages sent after last dossier reconciliation.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"conversation_id": map[string]any{
						"type":        "string",
						"description": "ID of the tracked conversation.",
					},
					"contact_name": map[string]any{
						"type":        "string",
						"description": "Full name or slug of the contact.",
					},
				},
			},
		},
		{
			Name:        "search_conversation_history",
			Description: "Executes Hybrid RRF query across sessions with optional date bounds and returns formatted transcripts.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{
						"type":        "string",
						"description": "Search query terms.",
					},
					"conversation_id": map[string]any{
						"type":        "string",
						"description": "Optional conversation ID to restrict search.",
					},
					"start_date": map[string]any{
						"type":        "string",
						"description": "ISO-8601 start timestamp filter.",
					},
					"end_date": map[string]any{
						"type":        "string",
						"description": "ISO-8601 end timestamp filter.",
					},
					"limit": map[string]any{
						"type":        "integer",
						"description": "Max sessions to return (default 10).",
					},
				},
				"required": []string{"query"},
			},
		},
		{
			Name:        "get_messages_around_time",
			Description: "Pulls verbatim message context and media OCR annotations within ±window_minutes of a target timestamp.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"conversation_id": map[string]any{
						"type":        "string",
						"description": "Conversation ID.",
					},
					"timestamp": map[string]any{
						"type":        "string",
						"description": "Target ISO-8601 timestamp.",
					},
					"window_minutes": map[string]any{
						"type":        "integer",
						"description": "Minutes before and after timestamp to fetch (default 30).",
					},
				},
				"required": []string{"conversation_id", "timestamp"},
			},
		},
		{
			Name:        "search_shared_media",
			Description: "Searches media attachments by OCR text and visual descriptions.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{
						"type":        "string",
						"description": "Keywords matching visual description or extracted OCR text.",
					},
					"conversation_id": map[string]any{
						"type":        "string",
						"description": "Optional conversation ID to restrict search.",
					},
					"limit": map[string]any{
						"type":        "integer",
						"description": "Max attachments to return (default 20).",
					},
				},
				"required": []string{"query"},
			},
		},
		{
			Name:        "manage_tracked_contact",
			Description: "Enables/disables thread tracking, initiates historical pagination backfill, or forces immediate dossier refresh.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"conversation_id": map[string]any{
						"type":        "string",
						"description": "Target conversation ID.",
					},
					"action": map[string]any{
						"type":        "string",
						"enum":        []string{"track", "untrack", "backfill", "refresh_dossier"},
						"description": "Action to perform on the conversation.",
					},
				},
				"required": []string{"conversation_id", "action"},
			},
		},
		{
			Name:        "get_session_antecedents",
			Description: "Retrieves causal antecedent sessions linked to a given session via open loop resolution or explicit replies.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"session_id": map[string]any{
						"type":        "integer",
						"description": "ID of the source conversation session.",
					},
				},
				"required": []string{"session_id"},
			},
		},
	}

	if enableSendMessage {
		tools = append(tools, Tool{
			Name:        "send_message",
			Description: "Sends a text message, media attachment (base64), or emoji reaction (add/remove) into an existing conversation. Exactly one of text, media_base64, or reaction_emoji must be provided.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"conversation_id": map[string]any{
						"type":        "string",
						"description": "Target conversation ID.",
					},
					"text": map[string]any{
						"type":        "string",
						"description": "Plain text message body. Use this for a text send.",
					},
					"reply_to_message_id": map[string]any{
						"type":        "string",
						"description": "Optional message ID to reply to (text and media sends).",
					},
					"media_base64": map[string]any{
						"type":        "string",
						"description": "Base64-encoded media bytes. Use this for a media send.",
					},
					"media_file_name": map[string]any{
						"type":        "string",
						"description": "File name for the media attachment (default: attachment).",
					},
					"media_mime_type": map[string]any{
						"type":        "string",
						"description": "MIME type for the media attachment (default: application/octet-stream).",
					},
					"caption": map[string]any{
						"type":        "string",
						"description": "Optional caption sent alongside media.",
					},
					"reaction_message_id": map[string]any{
						"type":        "string",
						"description": "Message ID to react to. Use this for a reaction send.",
					},
					"reaction_emoji": map[string]any{
						"type":        "string",
						"description": "Emoji to add as a reaction.",
					},
					"reaction_remove": map[string]any{
						"type":        "boolean",
						"description": "If true, remove the reaction instead of adding it.",
					},
				},
				"required": []string{"conversation_id"},
			},
		})
	}

	return tools
}
