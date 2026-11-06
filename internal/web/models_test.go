package web_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spelech/gmessage-rag/internal/config"
	"github.com/spelech/gmessage-rag/internal/web"
)

// [Requirement(UI-08)] GET returns the selection and gateway models; PUT
// persists to app_settings and hot-reloads the registry.
func TestModelsHandler_GetAndPut(t *testing.T) {
	database := openSendTestDB(t)
	ctx := context.Background()

	keys := []string{
		config.SettingKeySummaryModel,
		config.SettingKeyDossierModel,
		config.SettingKeyVisionModel,
		config.SettingKeyEmbeddingModel,
	}
	clean := func() {
		_, _ = database.Pool.Exec(context.Background(), `DELETE FROM app_settings WHERE key = ANY($1)`, keys)
	}
	clean()
	t.Cleanup(clean)

	registry := config.NewModelRegistry(config.ModelSettings{
		SummaryModel:   "s1",
		DossierModel:   "d1",
		VisionModel:    "v1",
		EmbeddingModel: "e1",
	})
	catalog := func(context.Context) ([]web.ModelOption, error) {
		return []web.ModelOption{{ID: "s1"}, {ID: "v1", SupportsVision: true}, {ID: "m2"}}, nil
	}

	handler := web.NewAPIHandlerWithModels(database, nil, nil, false, registry, catalog)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/models", handler.HandleModels)

	// GET returns current selection and advertised models.
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/models", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status %d: %s", rec.Code, rec.Body.String())
	}
	var got web.ModelsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode GET: %v", err)
	}
	if got.Selected.SummaryModel != "s1" {
		t.Errorf("selected summary = %q, want s1", got.Selected.SummaryModel)
	}
	if len(got.Available) != 3 {
		t.Errorf("available count = %d, want 3", len(got.Available))
	}

	// PUT persists and hot-reloads.
	body := `{"summary_model":"m2","dossier_model":"d1","vision_model":"v1","embedding_model":"e1"}`
	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/models", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT status %d: %s", rec.Code, rec.Body.String())
	}
	if registry.SummaryModel() != "m2" {
		t.Fatalf("registry not hot-reloaded: %q", registry.SummaryModel())
	}
	val, ok, err := database.GetSetting(ctx, config.SettingKeySummaryModel)
	if err != nil || !ok || val != "m2" {
		t.Fatalf("setting not persisted: ok=%v val=%q err=%v", ok, val, err)
	}

	// PUT with a blank role is rejected.
	bad := `{"summary_model":"","dossier_model":"d1","vision_model":"v1","embedding_model":"e1"}`
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, "/api/v1/models", strings.NewReader(bad))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for blank model, got %d", rec.Code)
	}

	// GET without a catalog still returns the selection, with a warning.
	noCatalog := web.NewAPIHandlerWithModels(database, nil, nil, false, registry, nil)
	mux2 := http.NewServeMux()
	mux2.HandleFunc("/api/v1/models", noCatalog.HandleModels)
	rec = httptest.NewRecorder()
	mux2.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/models", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET without catalog status %d", rec.Code)
	}
}

func TestModelsHandler_RegistryNotConfigured(t *testing.T) {
	handler := web.NewAPIHandlerWithModels(nil, nil, nil, false, nil, nil)
	rec := httptest.NewRecorder()
	handler.HandleModels(rec, httptest.NewRequest(http.MethodGet, "/api/v1/models", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
}
