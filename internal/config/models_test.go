package config_test

import (
	"errors"
	"testing"

	"github.com/spelech/gmessage-rag/internal/config"
)

func TestModelSettings_Validate(t *testing.T) {
	valid := config.ModelSettings{
		SummaryModel:   "s",
		DossierModel:   "d",
		VisionModel:    "v",
		EmbeddingModel: "e",
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("expected valid settings, got %v", err)
	}

	blank := valid
	blank.VisionModel = "  "
	if err := blank.Validate(); !errors.Is(err, config.ErrEmptyModel) {
		t.Fatalf("expected ErrEmptyModel for blank field, got %v", err)
	}
}

func TestModelSettings_WithDefaults(t *testing.T) {
	got := (config.ModelSettings{}).WithDefaults()
	if got.SummaryModel != config.DefaultSummaryModel {
		t.Errorf("summary default = %q, want %q", got.SummaryModel, config.DefaultSummaryModel)
	}
	if got.DossierModel != config.DefaultDossierModel {
		t.Errorf("dossier default = %q, want %q", got.DossierModel, config.DefaultDossierModel)
	}
	if got.VisionModel != config.DefaultVisionModel {
		t.Errorf("vision default = %q, want %q", got.VisionModel, config.DefaultVisionModel)
	}
	if got.EmbeddingModel != config.DefaultEmbeddingModel {
		t.Errorf("embedding default = %q, want %q", got.EmbeddingModel, config.DefaultEmbeddingModel)
	}
}

func TestModelSettings_MapRoundTrip(t *testing.T) {
	settings := config.ModelSettings{
		SummaryModel:   "summary-x",
		DossierModel:   "dossier-x",
		VisionModel:    "vision-x",
		EmbeddingModel: "embed-x",
	}
	stored := settings.ToMap()
	if len(stored) != 4 {
		t.Fatalf("expected 4 stored keys, got %d", len(stored))
	}

	// Partial stored map overlays only matching keys.
	base := config.ModelSettings{
		SummaryModel:   "base-s",
		DossierModel:   "base-d",
		VisionModel:    "base-v",
		EmbeddingModel: "base-e",
	}
	partial := map[string]string{config.SettingKeySummaryModel: "stored-s"}
	out := config.ModelSettingsFromMap(base, partial)
	if out.SummaryModel != "stored-s" {
		t.Errorf("summary overlay = %q, want stored-s", out.SummaryModel)
	}
	if out.DossierModel != "base-d" {
		t.Errorf("dossier should retain base value, got %q", out.DossierModel)
	}
}

func TestModelRegistry_HotReload(t *testing.T) {
	registry := config.NewModelRegistry(config.ModelSettings{})
	if registry.SummaryModel() != config.DefaultSummaryModel {
		t.Fatalf("expected default summary model, got %q", registry.SummaryModel())
	}

	next := config.ModelSettings{
		SummaryModel:   "new-summary",
		DossierModel:   "new-dossier",
		VisionModel:    "new-vision",
		EmbeddingModel: "new-embedding",
	}
	if err := registry.Set(next); err != nil {
		t.Fatalf("set: %v", err)
	}
	if registry.SummaryModel() != "new-summary" {
		t.Fatalf("hot reload failed: got %q", registry.SummaryModel())
	}

	if err := registry.Set(config.ModelSettings{}); !errors.Is(err, config.ErrEmptyModel) {
		t.Fatalf("expected ErrEmptyModel on invalid set, got %v", err)
	}
	// Failed update must not clobber the previous value.
	if registry.SummaryModel() != "new-summary" {
		t.Fatalf("invalid set mutated state: got %q", registry.SummaryModel())
	}
}
