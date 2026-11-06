package mcpserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spelech/gmessage-rag/internal/db"
	"github.com/spelech/gmessage-rag/internal/gm"
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
	GetConversation(ctx context.Context, id string) (*db.Conversation, error)
	AddReaction(ctx context.Context, messageID, sender, emoji string) error
	RemoveReaction(ctx context.Context, messageID, sender, emoji string) error
	GetLinksForSession(ctx context.Context, sessionID int64) ([]db.MessageLink, error)
	GetAntecedentSessions(ctx context.Context, sourceSessionIDs []int64) ([]db.ConversationSession, error)
}

// errDatabaseNotConfigured is returned when a tool is invoked without a database.
var errDatabaseNotConfigured = errors.New("database is not configured for the MCP server")

// toolHandlers binds the MCP tool surface to a persistence layer and an
// optional outbound message transport.
type toolHandlers struct {
	database Database
	sender   MessageSender
}

// builtinHandler selects the handler for a builtin tool name.
func builtinHandler(name string, database Database, sender MessageSender) ToolHandler {
	h := &toolHandlers{database: database, sender: sender}
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
	case "get_session_antecedents":
		return h.getSessionAntecedents
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

// maxOutboundMediaBytes caps decoded media accepted by send_message so a
// single tool call cannot exhaust memory or the upload path.
const maxOutboundMediaBytes = 10 << 20 // 10 MiB

// sendMessageArg is the union request accepted by the send_message tool.
type sendMessageArg struct {
	ConversationID    string `json:"conversation_id"`
	Text              string `json:"text"`
	ReplyToMessageID  string `json:"reply_to_message_id"`
	MediaBase64       string `json:"media_base64"`
	MediaFileName     string `json:"media_file_name"`
	MediaMimeType     string `json:"media_mime_type"`
	Caption           string `json:"caption"`
	ReactionMessageID string `json:"reaction_message_id"`
	ReactionEmoji     string `json:"reaction_emoji"`
	ReactionRemove    bool   `json:"reaction_remove"`
}

// sendMessage dispatches one outbound send: reaction, media, or text. The
// handler fails closed when no transport is wired. Reaction sends persist the
// change explicitly because message upserts use ON CONFLICT DO NOTHING; text
// and media persistence relies on the outgoing event echo.
func (h *toolHandlers) sendMessage(ctx context.Context, args json.RawMessage) (*CallToolResult, error) {
	if h.sender == nil {
		return nil, errors.New("send_message is unavailable: no outbound transport is configured")
	}

	var a sendMessageArg
	if err := decodeArgs(args, &a); err != nil {
		return nil, err
	}

	conversationID := strings.TrimSpace(a.ConversationID)
	if conversationID == "" {
		return nil, errors.New("conversation_id is required")
	}

	reactionMessageID := strings.TrimSpace(a.ReactionMessageID)
	hasReaction := reactionMessageID != ""
	hasMedia := strings.TrimSpace(a.MediaBase64) != ""
	hasText := strings.TrimSpace(a.Text) != ""

	modes := 0
	if hasReaction {
		modes++
	}
	if hasMedia {
		modes++
	}
	if hasText {
		modes++
	}
	if modes == 0 {
		return nil, errors.New("provide text, media_base64, or reaction_message_id")
	}
	if modes > 1 {
		return nil, errors.New("provide only one of text, media_base64, or reaction_message_id per call")
	}

	// Reaction branch: send first, then persist the reaction locally.
	if hasReaction {
		emoji := strings.TrimSpace(a.ReactionEmoji)
		if emoji == "" {
			return nil, errors.New("reaction_emoji is required when reaction_message_id is set")
		}
		if h.database == nil {
			return nil, errDatabaseNotConfigured
		}

		if err := h.sender.SendReaction(ctx, reactionMessageID, emoji, a.ReactionRemove, nil); err != nil {
			return nil, fmt.Errorf("send reaction: %w", err)
		}

		var persistErr error
		if a.ReactionRemove {
			persistErr = h.database.RemoveReaction(ctx, reactionMessageID, "me", emoji)
		} else {
			persistErr = h.database.AddReaction(ctx, reactionMessageID, "me", emoji)
		}
		if persistErr != nil {
			return nil, fmt.Errorf("reaction sent but persistence failed: %w", persistErr)
		}

		action := "added"
		if a.ReactionRemove {
			action = "removed"
		}
		return jsonToolResult(map[string]any{
			"conversation_id": conversationID,
			"message_id":      reactionMessageID,
			"emoji":           emoji,
			"action":          action,
			"status":          "sent",
		})
	}

	// Media branch: base64 decode with a size cap, then upload/send.
	if hasMedia {
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(a.MediaBase64))
		if err != nil {
			return nil, fmt.Errorf("media_base64 is not valid base64: %w", err)
		}
		if len(decoded) == 0 {
			return nil, errors.New("media_base64 decoded to zero bytes")
		}
		if len(decoded) > maxOutboundMediaBytes {
			return nil, fmt.Errorf("media exceeds %d byte limit", maxOutboundMediaBytes)
		}

		res, err := h.sender.SendMedia(ctx, gm.OutgoingMedia{
			ConversationID: conversationID,
			FileName:       strings.TrimSpace(a.MediaFileName),
			MimeType:       strings.TrimSpace(a.MediaMimeType),
			Data:           decoded,
			Caption:        strings.TrimSpace(a.Caption),
			ReplyToID:      strings.TrimSpace(a.ReplyToMessageID),
		})
		if err != nil {
			return nil, fmt.Errorf("send media: %w", err)
		}
		return jsonToolResult(res)
	}

	// Text branch.
	res, err := h.sender.SendText(ctx, gm.OutgoingMessage{
		ConversationID: conversationID,
		Text:           strings.TrimSpace(a.Text),
		ReplyToID:      strings.TrimSpace(a.ReplyToMessageID),
	})
	if err != nil {
		return nil, fmt.Errorf("send text: %w", err)
	}
	return jsonToolResult(res)
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

type sessionAntecedentsArgs struct {
	SessionID int64 `json:"session_id"`
}

func (h *toolHandlers) getSessionAntecedents(ctx context.Context, args json.RawMessage) (*CallToolResult, error) {
	if h.database == nil {
		return nil, errDatabaseNotConfigured
	}
	var a sessionAntecedentsArgs
	if err := decodeArgs(args, &a); err != nil {
		return nil, err
	}
	if a.SessionID <= 0 {
		return nil, errors.New("session_id must be a positive integer")
	}

	links, err := h.database.GetLinksForSession(ctx, a.SessionID)
	if err != nil {
		return nil, fmt.Errorf("get links for session: %w", err)
	}
	if links == nil {
		links = []db.MessageLink{}
	}

	antecedents, err := h.database.GetAntecedentSessions(ctx, []int64{a.SessionID})
	if err != nil {
		return nil, fmt.Errorf("get antecedent sessions: %w", err)
	}
	if antecedents == nil {
		antecedents = []db.ConversationSession{}
	}

	return jsonToolResult(map[string]any{
		"session_id":          a.SessionID,
		"links":               links,
		"antecedent_sessions": antecedents,
		"count":               len(antecedents),
	})
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
