package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spelech/gmessage-rag/internal/db"
)

// Database defines the persistence operations consumed by the MCP tool handlers.
// Keep this interface narrow and DB-shaped so tools stay thin API wrappers.
type Database interface {
	ListConversations(ctx context.Context, trackedOnly bool, limit int) ([]db.Conversation, error)
	SetConversationTracked(ctx context.Context, conversationID string, isTracked bool) error
	ResetConversationBackfill(ctx context.Context, conversationID string) error
	GetContactDossier(ctx context.Context, contactIdentifier string) (*db.ContactDossier, error)
	GetDossierLiveTail(ctx context.Context, conversationID string, lastProcessedAt *time.Time) ([]db.Message, error)
	SearchSessionsHybrid(ctx context.Context, conversationID, queryText string, queryVec []float32, dateFrom, dateTo *time.Time, limit int) ([]db.SessionSearchResult, error)
	SearchSessionsHybridAllConversations(ctx context.Context, queryText string, queryVec []float32, dateFrom, dateTo *time.Time, limit int) ([]db.SessionSearchResult, error)
	GetMessagesAroundTime(ctx context.Context, conversationID string, targetTime time.Time, windowMinutes int) ([]db.Message, error)
	GetMediaAttachmentsForMessage(ctx context.Context, messageID string) ([]db.MediaAttachment, error)
	SearchMediaAttachments(ctx context.Context, conversationID, query string, limit int) ([]db.MediaAttachment, error)
}

// errDatabaseNotConfigured is returned when a tool is invoked without a database.
var errDatabaseNotConfigured = errors.New("database is not configured for the MCP server")

// toolHandlers binds the MCP tool surface to a persistence layer.
type toolHandlers struct {
	database Database
}

// builtinHandler selects the DB-backed handler for a builtin tool name.
func builtinHandler(name string, database Database) ToolHandler {
	h := &toolHandlers{database: database}
	switch name {
	case "list_conversations":
		return h.listConversations
	case "get_contact_dossier":
		return h.getContactDossier
	case "search_conversation_history":
		return h.searchConversationHistory
	case "get_messages_around_time":
		return h.getMessagesAroundTime
	case "search_shared_media":
		return h.searchSharedMedia
	case "manage_tracked_contact":
		return h.manageTrackedContact
	case "send_message":
		return h.sendMessage
	default:
		return h.unknownTool(name)
	}
}

func (h *toolHandlers) unknownTool(name string) ToolHandler {
	return func(context.Context, json.RawMessage) (*CallToolResult, error) {
		return nil, fmt.Errorf("no handler registered for tool %q", name)
	}
}

// sendMessage fails closed: the outbound transport is not wired into the MCP server.
func (h *toolHandlers) sendMessage(context.Context, json.RawMessage) (*CallToolResult, error) {
	return nil, errors.New("send_message is unavailable: no outbound transport is configured")
}

type listConversationsArgs struct {
	Limit       int  `json:"limit"`
	TrackedOnly bool `json:"tracked_only"`
}

func (h *toolHandlers) listConversations(ctx context.Context, args json.RawMessage) (*CallToolResult, error) {
	if h.database == nil {
		return nil, errDatabaseNotConfigured
	}
	var a listConversationsArgs
	if err := decodeArgs(args, &a); err != nil {
		return nil, err
	}
	limit := a.Limit
	if limit <= 0 {
		limit = 50
	}

	conversations, err := h.database.ListConversations(ctx, a.TrackedOnly, limit)
	if err != nil {
		return nil, fmt.Errorf("list conversations: %w", err)
	}
	if conversations == nil {
		conversations = []db.Conversation{}
	}

	return jsonToolResult(map[string]any{
		"conversations": conversations,
		"count":         len(conversations),
	})
}

type contactDossierArgs struct {
	ConversationID string `json:"conversation_id"`
	ContactName    string `json:"contact_name"`
}

func (h *toolHandlers) getContactDossier(ctx context.Context, args json.RawMessage) (*CallToolResult, error) {
	if h.database == nil {
		return nil, errDatabaseNotConfigured
	}
	var a contactDossierArgs
	if err := decodeArgs(args, &a); err != nil {
		return nil, err
	}

	identifier := strings.TrimSpace(a.ConversationID)
	if identifier == "" {
		identifier = strings.TrimSpace(a.ContactName)
	}
	if identifier == "" {
		return nil, errors.New("conversation_id or contact_name is required")
	}

	dossier, err := h.database.GetContactDossier(ctx, identifier)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			return nil, fmt.Errorf("contact dossier not found for %q", identifier)
		}
		return nil, fmt.Errorf("get contact dossier: %w", err)
	}

	tail, err := h.database.GetDossierLiveTail(ctx, dossier.ConversationID, dossier.LastProcessedMessageAt)
	if err != nil {
		return nil, fmt.Errorf("get dossier live tail: %w", err)
	}
	if tail == nil {
		tail = []db.Message{}
	}

	return jsonToolResult(map[string]any{
		"dossier":         dossier,
		"live_tail":       tail,
		"live_tail_count": len(tail),
	})
}

type searchHistoryArgs struct {
	Query          string `json:"query"`
	ConversationID string `json:"conversation_id"`
	StartDate      string `json:"start_date"`
	EndDate        string `json:"end_date"`
	Limit          int    `json:"limit"`
}

func (h *toolHandlers) searchConversationHistory(ctx context.Context, args json.RawMessage) (*CallToolResult, error) {
	if h.database == nil {
		return nil, errDatabaseNotConfigured
	}
	var a searchHistoryArgs
	if err := decodeArgs(args, &a); err != nil {
		return nil, err
	}

	query := strings.TrimSpace(a.Query)
	if query == "" {
		return nil, errors.New("query is required")
	}

	dateFrom, err := parseOptionalTime(a.StartDate)
	if err != nil {
		return nil, fmt.Errorf("invalid start_date: %w", err)
	}
	dateTo, err := parseOptionalTime(a.EndDate)
	if err != nil {
		return nil, fmt.Errorf("invalid end_date: %w", err)
	}

	limit := a.Limit
	if limit <= 0 {
		limit = 10
	}

	var sessions []db.SessionSearchResult
	var searchErr error
	scope := strings.TrimSpace(a.ConversationID)
	if scope == "" {
		sessions, searchErr = h.database.SearchSessionsHybridAllConversations(ctx, query, nil, dateFrom, dateTo, limit)
	} else {
		sessions, searchErr = h.database.SearchSessionsHybrid(ctx, scope, query, nil, dateFrom, dateTo, limit)
	}
	if searchErr != nil {
		return nil, fmt.Errorf("search conversation history: %w", searchErr)
	}
	if sessions == nil {
		sessions = []db.SessionSearchResult{}
	}

	return jsonToolResult(map[string]any{
		"query":    query,
		"sessions": sessions,
		"count":    len(sessions),
	})
}

type messagesAroundTimeArgs struct {
	ConversationID string `json:"conversation_id"`
	Timestamp      string `json:"timestamp"`
	WindowMinutes  int    `json:"window_minutes"`
}

func (h *toolHandlers) getMessagesAroundTime(ctx context.Context, args json.RawMessage) (*CallToolResult, error) {
	if h.database == nil {
		return nil, errDatabaseNotConfigured
	}
	var a messagesAroundTimeArgs
	if err := decodeArgs(args, &a); err != nil {
		return nil, err
	}

	conversationID := strings.TrimSpace(a.ConversationID)
	if conversationID == "" {
		return nil, errors.New("conversation_id is required")
	}

	target, err := time.Parse(time.RFC3339, strings.TrimSpace(a.Timestamp))
	if err != nil {
		return nil, fmt.Errorf("invalid timestamp (want RFC3339): %w", err)
	}

	window := a.WindowMinutes
	if window <= 0 {
		window = 30
	}

	messages, err := h.database.GetMessagesAroundTime(ctx, conversationID, target, window)
	if err != nil {
		return nil, fmt.Errorf("get messages around time: %w", err)
	}

	type annotatedMessage struct {
		Message db.Message           `json:"message"`
		Media   []db.MediaAttachment `json:"media"`
	}

	annotated := make([]annotatedMessage, 0, len(messages))
	for _, msg := range messages {
		media, err := h.database.GetMediaAttachmentsForMessage(ctx, msg.ID)
		if err != nil {
			return nil, fmt.Errorf("get media for message %s: %w", msg.ID, err)
		}
		if media == nil {
			media = []db.MediaAttachment{}
		}
		annotated = append(annotated, annotatedMessage{Message: msg, Media: media})
	}

	return jsonToolResult(map[string]any{
		"conversation_id": conversationID,
		"messages":        annotated,
		"count":           len(annotated),
	})
}

type sharedMediaArgs struct {
	Query          string `json:"query"`
	ConversationID string `json:"conversation_id"`
	Limit          int    `json:"limit"`
}

func (h *toolHandlers) searchSharedMedia(ctx context.Context, args json.RawMessage) (*CallToolResult, error) {
	if h.database == nil {
		return nil, errDatabaseNotConfigured
	}
	var a sharedMediaArgs
	if err := decodeArgs(args, &a); err != nil {
		return nil, err
	}

	query := strings.TrimSpace(a.Query)
	if query == "" {
		return nil, errors.New("query is required")
	}

	limit := a.Limit
	if limit <= 0 {
		limit = 20
	}

	attachments, err := h.database.SearchMediaAttachments(ctx, strings.TrimSpace(a.ConversationID), query, limit)
	if err != nil {
		return nil, fmt.Errorf("search shared media: %w", err)
	}
	if attachments == nil {
		attachments = []db.MediaAttachment{}
	}

	return jsonToolResult(map[string]any{
		"attachments": attachments,
		"count":       len(attachments),
	})
}

type manageTrackedContactArgs struct {
	ConversationID string `json:"conversation_id"`
	Action         string `json:"action"`
}

func (h *toolHandlers) manageTrackedContact(ctx context.Context, args json.RawMessage) (*CallToolResult, error) {
	if h.database == nil {
		return nil, errDatabaseNotConfigured
	}
	var a manageTrackedContactArgs
	if err := decodeArgs(args, &a); err != nil {
		return nil, err
	}

	conversationID := strings.TrimSpace(a.ConversationID)
	if conversationID == "" {
		return nil, errors.New("conversation_id is required")
	}
	action := strings.ToLower(strings.TrimSpace(a.Action))

	result := map[string]any{
		"conversation_id": conversationID,
		"action":          action,
	}

	switch action {
	case "track", "untrack":
		isTracked := action == "track"
		if err := h.database.SetConversationTracked(ctx, conversationID, isTracked); err != nil {
			if errors.Is(err, db.ErrNotFound) {
				return nil, fmt.Errorf("conversation %q not found", conversationID)
			}
			return nil, fmt.Errorf("%s conversation: %w", action, err)
		}
		result["is_tracked"] = isTracked
	case "backfill":
		if err := h.database.ResetConversationBackfill(ctx, conversationID); err != nil {
			if errors.Is(err, db.ErrNotFound) {
				return nil, fmt.Errorf("conversation %q not found", conversationID)
			}
			return nil, fmt.Errorf("reset conversation backfill: %w", err)
		}
		result["backfill_cursor"] = nil
		result["backfill_complete"] = false
	case "refresh_dossier":
		dossier, err := h.database.GetContactDossier(ctx, conversationID)
		if err != nil {
			if errors.Is(err, db.ErrNotFound) {
				return nil, fmt.Errorf("contact dossier not found for %q", conversationID)
			}
			return nil, fmt.Errorf("refresh dossier: %w", err)
		}
		tail, err := h.database.GetDossierLiveTail(ctx, dossier.ConversationID, dossier.LastProcessedMessageAt)
		if err != nil {
			return nil, fmt.Errorf("refresh dossier live tail: %w", err)
		}
		if tail == nil {
			tail = []db.Message{}
		}
		result["dossier"] = dossier
		result["live_tail_count"] = len(tail)
	default:
		return nil, fmt.Errorf("unsupported action %q (want track, untrack, backfill, refresh_dossier)", action)
	}

	return jsonToolResult(result)
}

// decodeArgs unmarshals tool arguments, tolerating an omitted arguments field.
func decodeArgs(raw json.RawMessage, target any) error {
	if len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return fmt.Errorf("invalid tool arguments: %w", err)
	}
	return nil
}

func parseOptionalTime(value string) (*time.Time, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, trimmed)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

// jsonToolResult marshals a payload as sanitized untrusted-data text content.
func jsonToolResult(payload any) (*CallToolResult, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal tool result: %w", err)
	}
	return &CallToolResult{
		Content: []ToolContent{
			{
				Type: "text",
				Text: WrapUntrustedData(string(encoded)),
			},
		},
	}, nil
}
