package web

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"sort"
	"time"

	"github.com/spelech/gmessage-rag/internal/db"
	"github.com/spelech/gmessage-rag/internal/mcpserver"
)

// defaultBriefingLookback is the window applied when the caller omits the since parameter.
// It approximates a "last read" fallback for Home Assistant REST sensors (TRANS-03).
const defaultBriefingLookback = 24 * time.Hour

// BriefingItem describes what changed for one tracked contact since the requested timestamp.
// Dossier-derived text (markdown) is wrapped in fail-closed untrusted-data boundaries.
type BriefingItem struct {
	ConversationID   string         `json:"conversation_id"`
	ContactName      string         `json:"contact_name"`
	CurrentVersion   int            `json:"current_version"`
	PreviousVersion  *int           `json:"previous_version,omitempty"`
	ChangedAt        time.Time      `json:"changed_at"`
	ChangedFields    []string       `json:"changed_fields"`
	MarkdownChanged  bool           `json:"markdown_changed"`
	CurrentMarkdown  string         `json:"current_markdown"`
	PreviousMarkdown string         `json:"previous_markdown,omitempty"`
	CurrentState     map[string]any `json:"current_state"`
}

// BriefingResponse is the payload for GET /api/v1/briefing.
type BriefingResponse struct {
	Since       time.Time      `json:"since"`
	GeneratedAt time.Time      `json:"generated_at"`
	Count       int            `json:"count"`
	Items       []BriefingItem `json:"items"`
}

// HandleBriefing handles GET /api/v1/briefing and GET /api/v1/briefing/today.
// It reports per tracked contact what changed since the caller-supplied RFC3339 since parameter,
// or a default lookback window when omitted.
// Requirement ID: TRANS-03
func (h *APIHandler) HandleBriefing(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	since, err := parseBriefingSince(r.URL.Query().Get("since"))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	if h.database == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "database unavailable"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	changes, err := h.database.ListDossierChangesSince(ctx, since)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	items := make([]BriefingItem, 0, len(changes))
	for _, change := range changes {
		items = append(items, briefingItemFromChange(change))
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(BriefingResponse{
		Since:       since,
		GeneratedAt: time.Now().UTC(),
		Count:       len(items),
		Items:       items,
	})
}

// parseBriefingSince resolves the since query parameter, defaulting to a lookback window.
func parseBriefingSince(raw string) (time.Time, error) {
	if raw == "" {
		return time.Now().UTC().Add(-defaultBriefingLookback), nil
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, &briefingParamError{message: "invalid since parameter: must be RFC3339"}
	}
	return parsed.UTC(), nil
}

// briefingParamError marks client-supplied parameter validation failures.
type briefingParamError struct {
	message string
}

func (e *briefingParamError) Error() string { return e.message }

// briefingItemFromChange projects a persisted dossier change into the API item shape,
// wrapping dossier-derived markdown in untrusted-data boundaries.
func briefingItemFromChange(change db.DossierChange) BriefingItem {
	return BriefingItem{
		ConversationID:   change.ConversationID,
		ContactName:      change.ContactName,
		CurrentVersion:   change.CurrentVersion,
		PreviousVersion:  change.PreviousVersion,
		ChangedAt:        change.ChangedAt,
		ChangedFields:    computeChangedFields(change.PreviousState, change.CurrentState),
		MarkdownChanged:  change.PreviousVersion != nil && change.PreviousMarkdown != change.CurrentMarkdown,
		CurrentMarkdown:  mcpserver.WrapUntrustedData(change.CurrentMarkdown),
		PreviousMarkdown: wrapOptionalMarkdown(change.PreviousMarkdown),
		CurrentState:     change.CurrentState,
	}
}

// wrapOptionalMarkdown wraps non-empty markdown, returning empty for absent prior state.
func wrapOptionalMarkdown(markdown string) string {
	if markdown == "" {
		return ""
	}
	return mcpserver.WrapUntrustedData(markdown)
}

// computeChangedFields returns the sorted structured-state keys that were added, removed, or modified.
func computeChangedFields(prev, current map[string]any) []string {
	changed := make(map[string]struct{})
	for key, value := range current {
		prior, ok := prev[key]
		if !ok || !reflect.DeepEqual(prior, value) {
			changed[key] = struct{}{}
		}
	}
	for key := range prev {
		if _, ok := current[key]; !ok {
			changed[key] = struct{}{}
		}
	}

	fields := make([]string, 0, len(changed))
	for key := range changed {
		fields = append(fields, key)
	}
	sort.Strings(fields)
	return fields
}
