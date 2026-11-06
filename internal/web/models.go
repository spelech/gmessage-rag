package web

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/spelech/gmessage-rag/internal/config"
)

// ModelOption describes one model available for selection in the UI.
type ModelOption struct {
	ID             string `json:"id"`
	SupportsVision bool   `json:"supports_vision"`
	Mode           string `json:"mode,omitempty"`
	Provider       string `json:"provider,omitempty"`
}

// ModelCatalog returns the models advertised by the LLM gateway.
type ModelCatalog func(ctx context.Context) ([]ModelOption, error)

// ModelsResponse is the payload for GET /api/v1/models.
type ModelsResponse struct {
	Selected  config.ModelSettings `json:"selected"`
	Available []ModelOption        `json:"available"`
	Warning   string               `json:"warning,omitempty"`
}

// HandleModels handles GET and PUT /api/v1/models.
//
// GET returns the current selection plus the models advertised by the gateway.
// PUT validates and persists a new selection, applying it immediately.
func (h *APIHandler) HandleModels(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if h.models == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "model registry not configured"})
		return
	}

	switch r.Method {
	case http.MethodGet:
		resp := ModelsResponse{Selected: h.models.Get(), Available: []ModelOption{}}
		if h.catalog != nil {
			ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
			defer cancel()
			if available, err := h.catalog(ctx); err != nil {
				resp.Warning = "could not list gateway models: " + err.Error()
			} else {
				resp.Available = available
			}
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(resp)

	case http.MethodPut:
		var incoming config.ModelSettings
		if err := json.NewDecoder(r.Body).Decode(&incoming); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid json payload: " + err.Error()})
			return
		}
		if err := incoming.Validate(); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		if h.database != nil {
			if err := h.database.SetSettings(ctx, incoming.ToMap()); err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "persist settings: " + err.Error()})
				return
			}
		}

		if err := h.models.Set(incoming); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success":  true,
			"selected": h.models.Get(),
		})

	default:
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	}
}
