package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/spelech/gmessage-rag/internal/db"
	"github.com/spelech/gmessage-rag/internal/gm"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
)

// DefaultMaxStreamMediaBytes caps a single multipart media upload. The MCP
// base64 path keeps its own smaller 10 MiB cap because base64 over JSON-RPC
// inflates payloads; the multipart path streams the request body and avoids
// that overhead, so it can track the RCS ceiling more closely.
const DefaultMaxStreamMediaBytes int64 = 100 << 20 // 100 MiB

// MessageSender is the outbound transport consumed by the web send API. It is
// satisfied by *gm.Client and kept as an interface so handlers stay testable.
type MessageSender interface {
	SendText(ctx context.Context, msg gm.OutgoingMessage) (*gm.SendResult, error)
	SendMedia(ctx context.Context, msg gm.OutgoingMedia) (*gm.SendResult, error)
	SendReaction(ctx context.Context, messageID, emoji string, remove bool, simPayload *gmproto.SIMPayload) error
}

// SendHandler exposes human-facing outbound send endpoints alongside the MCP
// base64 tool. Text and reactions use JSON; media uses multipart/form-data so
// the file body streams to the server without base64 or JSON-RPC framing.
type SendHandler struct {
	sender        MessageSender
	database      *db.DB
	enabled       bool
	MaxMediaBytes int64
}

// NewSendHandler constructs a SendHandler. When enabled is false every request
// fails closed with 403; callers should also hide the UI surface.
func NewSendHandler(sender MessageSender, database *db.DB, enabled bool) *SendHandler {
	return &SendHandler{
		sender:        sender,
		database:      database,
		enabled:       enabled,
		MaxMediaBytes: DefaultMaxStreamMediaBytes,
	}
}

// RegisterRoutes registers the JSON and multipart send endpoints.
func (h *SendHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/v1/send", h.handleSend)
	mux.HandleFunc("/api/v1/send/media", h.handleSendMedia)
}

// SendEnabled reports whether outbound sending is available.
func (h *SendHandler) SendEnabled() bool {
	return h != nil && h.enabled && h.sender != nil
}

type sendJSONRequest struct {
	ConversationID    string `json:"conversation_id"`
	Text              string `json:"text"`
	ReplyToMessageID  string `json:"reply_to_message_id"`
	ReactionMessageID string `json:"reaction_message_id"`
	ReactionEmoji     string `json:"reaction_emoji"`
	ReactionRemove    bool   `json:"reaction_remove"`
}

// handleSend accepts JSON text or reaction sends. Media is handled by the
// multipart endpoint so large files never pass through base64.
func (h *SendHandler) handleSend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeSendError(w, http.StatusMethodNotAllowed, "method not allowed; POST required")
		return
	}
	if !h.SendEnabled() {
		writeSendError(w, http.StatusForbidden, "outbound send is disabled; set ENABLE_MCP_SEND_MESSAGE=true")
		return
	}

	var req sendJSONRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeSendError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON payload: %v", err))
		return
	}

	conversationID := strings.TrimSpace(req.ConversationID)
	if conversationID == "" {
		writeSendError(w, http.StatusBadRequest, "conversation_id is required")
		return
	}

	reactionMessageID := strings.TrimSpace(req.ReactionMessageID)
	text := strings.TrimSpace(req.Text)

	modes := 0
	if reactionMessageID != "" {
		modes++
	}
	if text != "" {
		modes++
	}
	if modes == 0 {
		writeSendError(w, http.StatusBadRequest, "provide text or reaction_message_id")
		return
	}
	if modes > 1 {
		writeSendError(w, http.StatusBadRequest, "provide only one of text or reaction_message_id per call")
		return
	}

	if reactionMessageID != "" {
		h.sendReaction(w, r, conversationID, reactionMessageID, req)
		return
	}

	res, err := h.sender.SendText(r.Context(), gm.OutgoingMessage{
		ConversationID: conversationID,
		Text:           text,
		ReplyToID:      strings.TrimSpace(req.ReplyToMessageID),
	})
	if err != nil {
		writeSendError(w, http.StatusBadGateway, fmt.Sprintf("send text: %v", err))
		return
	}
	writeSendJSON(w, http.StatusOK, res)
}

func (h *SendHandler) sendReaction(w http.ResponseWriter, r *http.Request, conversationID, messageID string, req sendJSONRequest) {
	emoji := strings.TrimSpace(req.ReactionEmoji)
	if emoji == "" {
		writeSendError(w, http.StatusBadRequest, "reaction_emoji is required when reaction_message_id is set")
		return
	}
	if h.database == nil {
		writeSendError(w, http.StatusServiceUnavailable, "database is not configured")
		return
	}

	if err := h.sender.SendReaction(r.Context(), messageID, emoji, req.ReactionRemove, nil); err != nil {
		writeSendError(w, http.StatusBadGateway, fmt.Sprintf("send reaction: %v", err))
		return
	}

	var persistErr error
	if req.ReactionRemove {
		persistErr = h.database.RemoveReaction(r.Context(), messageID, "me", emoji)
	} else {
		persistErr = h.database.AddReaction(r.Context(), messageID, "me", emoji)
	}
	if persistErr != nil {
		writeSendError(w, http.StatusInternalServerError, fmt.Sprintf("reaction sent but persistence failed: %v", persistErr))
		return
	}

	action := "added"
	if req.ReactionRemove {
		action = "removed"
	}
	writeSendJSON(w, http.StatusOK, map[string]any{
		"conversation_id": conversationID,
		"message_id":      messageID,
		"emoji":           emoji,
		"action":          action,
		"status":          "sent",
	})
}

// handleSendMedia accepts a multipart/form-data upload. The file part streams
// through the HTTP request; the server buffers it only at the libgm boundary,
// where UploadMedia requires a byte slice.
func (h *SendHandler) handleSendMedia(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeSendError(w, http.StatusMethodNotAllowed, "method not allowed; POST required")
		return
	}
	if !h.SendEnabled() {
		writeSendError(w, http.StatusForbidden, "outbound send is disabled; set ENABLE_MCP_SEND_MESSAGE=true")
		return
	}

	// Allow multipart framing overhead above the media cap itself.
	maxBody := h.MaxMediaBytes + (2 << 20)
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeSendError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("upload exceeds %d byte limit", h.MaxMediaBytes))
			return
		}
		writeSendError(w, http.StatusBadRequest, fmt.Sprintf("invalid multipart payload: %v", err))
		return
	}
	defer func() { _ = r.MultipartForm.RemoveAll() }()

	conversationID := strings.TrimSpace(r.FormValue("conversation_id"))
	if conversationID == "" {
		writeSendError(w, http.StatusBadRequest, "conversation_id is required")
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		file, header, err = r.FormFile("media")
	}
	if err != nil {
		writeSendError(w, http.StatusBadRequest, "multipart field 'file' is required")
		return
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, h.MaxMediaBytes+1))
	if err != nil {
		writeSendError(w, http.StatusBadRequest, fmt.Sprintf("read upload: %v", err))
		return
	}
	if int64(len(data)) > h.MaxMediaBytes {
		writeSendError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("media exceeds %d byte limit", h.MaxMediaBytes))
		return
	}
	if len(data) == 0 {
		writeSendError(w, http.StatusBadRequest, "uploaded file is empty")
		return
	}

	mimeType := strings.TrimSpace(r.FormValue("mime_type"))
	if mimeType == "" {
		mimeType = header.Header.Get("Content-Type")
	}

	res, err := h.sender.SendMedia(r.Context(), gm.OutgoingMedia{
		ConversationID: conversationID,
		FileName:       header.Filename,
		MimeType:       mimeType,
		Data:           data,
		Caption:        strings.TrimSpace(r.FormValue("caption")),
		ReplyToID:      strings.TrimSpace(r.FormValue("reply_to_message_id")),
	})
	if err != nil {
		writeSendError(w, http.StatusBadGateway, fmt.Sprintf("send media: %v", err))
		return
	}
	writeSendJSON(w, http.StatusOK, res)
}

func writeSendJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeSendError(w http.ResponseWriter, status int, msg string) {
	writeSendJSON(w, status, map[string]string{"error": msg})
}
