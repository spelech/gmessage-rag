package web

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/spelech/gmessage-rag/internal/config"
	"github.com/spelech/gmessage-rag/internal/db"
	"github.com/spelech/gmessage-rag/internal/gm"
)

// StatusResponse represents the response structure for GET /api/v1/status.
type StatusResponse struct {
	Status            string `json:"status"`
	DBConnected       bool   `json:"db_connected"`
	GMConnected       bool   `json:"gm_connected"`
	SendEnabled       bool   `json:"send_enabled"`
	UnindexedMessages int64  `json:"unindexed_messages"`
}

// TrackContactRequest represents the payload for POST /api/v1/contacts/track.
type TrackContactRequest struct {
	ConversationID string `json:"conversation_id"`
	IsTracked      bool   `json:"is_tracked"`
}

// APIHandler handles REST API calls under /api/v1/*.
type APIHandler struct {
	database    *db.DB
	gmClient    *gm.Client
	tuner       *config.RuntimeTuner
	sendEnabled bool
	models      *config.ModelRegistry
	catalog     ModelCatalog
}

// NewAPI is an alias for NewAPIHandler.
func NewAPI(database *db.DB, gmClient *gm.Client) *APIHandler {
	return NewAPIHandler(database, gmClient)
}

// NewAPIHandler constructs a new APIHandler.
func NewAPIHandler(database *db.DB, gmClient *gm.Client) *APIHandler {
	return NewAPIHandlerWithTuner(database, gmClient, nil)
}

// NewAPIHandlerWithTuner constructs a new APIHandler with an optional RuntimeTuner.
func NewAPIHandlerWithTuner(database *db.DB, gmClient *gm.Client, tuner *config.RuntimeTuner) *APIHandler {
	return NewAPIHandlerWithSend(database, gmClient, tuner, false)
}

// NewAPIHandlerWithSend constructs a new APIHandler and reports whether
// outbound send is available in the status payload.
func NewAPIHandlerWithSend(database *db.DB, gmClient *gm.Client, tuner *config.RuntimeTuner, sendEnabled bool) *APIHandler {
	return NewAPIHandlerWithModels(database, gmClient, tuner, sendEnabled, nil, nil)
}

// NewAPIHandlerWithModels constructs a new APIHandler with an optional model
// registry and gateway model catalog, enabling model selection endpoints.
func NewAPIHandlerWithModels(database *db.DB, gmClient *gm.Client, tuner *config.RuntimeTuner, sendEnabled bool, models *config.ModelRegistry, catalog ModelCatalog) *APIHandler {
	return &APIHandler{
		database:    database,
		gmClient:    gmClient,
		tuner:       tuner,
		sendEnabled: sendEnabled,
		models:      models,
		catalog:     catalog,
	}
}

// HandleStatus handles GET /api/v1/status.
func (h *APIHandler) HandleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	dbConnected := false
	var unindexedMessages int64

	if h.database != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if err := h.database.Ping(ctx); err == nil {
			dbConnected = true
			if count, err := h.database.CountUnindexedMessages(ctx); err == nil {
				unindexedMessages = count
			}
		}
	}

	gmConnected := false
	if h.gmClient != nil && h.gmClient.LibGM() != nil {
		gmConnected = h.gmClient.LibGM().IsLoggedIn()
	}

	status := "healthy"
	if !dbConnected {
		status = "degraded"
	}

	resp := StatusResponse{
		Status:            status,
		DBConnected:       dbConnected,
		GMConnected:       gmConnected,
		SendEnabled:       h.sendEnabled,
		UnindexedMessages: unindexedMessages,
	}

	_ = json.NewEncoder(w).Encode(resp)
}

// HandleContacts handles GET /api/v1/contacts.
func (h *APIHandler) HandleContacts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	if h.database == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "database unavailable"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	convs, err := h.database.ListConversations(ctx, false, 100)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	if convs == nil {
		convs = []db.Conversation{}
	}

	_ = json.NewEncoder(w).Encode(convs)
}

// HandleTrackContact handles POST /api/v1/contacts/track.
func (h *APIHandler) HandleTrackContact(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	if h.database == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "database unavailable"})
		return
	}

	var req TrackContactRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid request body: " + err.Error()})
		return
	}

	if req.ConversationID == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "conversation_id is required"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	if err := h.database.SetConversationTracked(ctx, req.ConversationID, req.IsTracked); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"conversation_id": req.ConversationID,
		"is_tracked":      req.IsTracked,
		"success":         true,
	})
}

// HandleTuner handles GET /api/v1/tuner and POST /api/v1/tuner.
func (h *APIHandler) HandleTuner(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if h.tuner == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "runtime tuner not configured"})
		return
	}

	switch r.Method {
	case http.MethodGet:
		settings := h.tuner.Get()
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(settings)

	case http.MethodPost:
		var newSettings config.TunerSettings
		if err := json.NewDecoder(r.Body).Decode(&newSettings); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid json payload: " + err.Error()})
			return
		}

		if err := h.tuner.Update(newSettings); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success":  true,
			"settings": h.tuner.Get(),
		})

	default:
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	}
}
