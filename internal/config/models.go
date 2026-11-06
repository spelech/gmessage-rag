package config

import (
	"errors"
	"strings"
	"sync"
)

// Default model selections. These are OpenAI-compatible model names served by
// the configured LiteLLM gateway. They intentionally avoid proprietary
// provider defaults so a fresh deployment does not fall back to a paid backend.
const (
	DefaultSummaryModel   = "deepseek-flash"
	DefaultDossierModel   = "deepseek-flash"
	DefaultVisionModel    = "glm-flash"
	DefaultEmbeddingModel = "text-embedding-3-small"
)

// Setting keys used to persist model selection in the app_settings table.
const (
	SettingKeySummaryModel   = "summary_model"
	SettingKeyDossierModel   = "dossier_model"
	SettingKeyVisionModel    = "vision_model"
	SettingKeyEmbeddingModel = "embedding_model"
)

// ErrEmptyModel is returned when a model selection is blank.
var ErrEmptyModel = errors.New("model selection cannot be empty")

// ModelSettings holds the model selected for each LLM pipeline role. The fields
// are tagged with the same names used as app_settings keys.
type ModelSettings struct {
	SummaryModel   string `json:"summary_model"`
	DossierModel   string `json:"dossier_model"`
	VisionModel    string `json:"vision_model"`
	EmbeddingModel string `json:"embedding_model"`
}

// Validate reports whether every role has a non-blank model name.
func (s ModelSettings) Validate() error {
	if strings.TrimSpace(s.SummaryModel) == "" ||
		strings.TrimSpace(s.DossierModel) == "" ||
		strings.TrimSpace(s.VisionModel) == "" ||
		strings.TrimSpace(s.EmbeddingModel) == "" {
		return ErrEmptyModel
	}
	return nil
}

// WithDefaults fills blank fields with the built-in defaults.
func (s ModelSettings) WithDefaults() ModelSettings {
	if strings.TrimSpace(s.SummaryModel) == "" {
		s.SummaryModel = DefaultSummaryModel
	}
	if strings.TrimSpace(s.DossierModel) == "" {
		s.DossierModel = DefaultDossierModel
	}
	if strings.TrimSpace(s.VisionModel) == "" {
		s.VisionModel = DefaultVisionModel
	}
	if strings.TrimSpace(s.EmbeddingModel) == "" {
		s.EmbeddingModel = DefaultEmbeddingModel
	}
	return s
}

// ToMap returns the settings keyed by their app_settings key names. Blank
// fields are omitted so a partial update never clears a stored value.
func (s ModelSettings) ToMap() map[string]string {
	m := make(map[string]string, 4)
	if v := strings.TrimSpace(s.SummaryModel); v != "" {
		m[SettingKeySummaryModel] = v
	}
	if v := strings.TrimSpace(s.DossierModel); v != "" {
		m[SettingKeyDossierModel] = v
	}
	if v := strings.TrimSpace(s.VisionModel); v != "" {
		m[SettingKeyVisionModel] = v
	}
	if v := strings.TrimSpace(s.EmbeddingModel); v != "" {
		m[SettingKeyEmbeddingModel] = v
	}
	return m
}

// ModelSettingsFromMap overlays stored app_settings values onto a base set.
func ModelSettingsFromMap(base ModelSettings, stored map[string]string) ModelSettings {
	out := base
	if v := strings.TrimSpace(stored[SettingKeySummaryModel]); v != "" {
		out.SummaryModel = v
	}
	if v := strings.TrimSpace(stored[SettingKeyDossierModel]); v != "" {
		out.DossierModel = v
	}
	if v := strings.TrimSpace(stored[SettingKeyVisionModel]); v != "" {
		out.VisionModel = v
	}
	if v := strings.TrimSpace(stored[SettingKeyEmbeddingModel]); v != "" {
		out.EmbeddingModel = v
	}
	return out
}

// ModelRegistry provides thread-safe, hot-reloadable model selection. Pipeline
// clients read the current selection on every request, so an update takes
// effect immediately without a restart.
type ModelRegistry struct {
	mu       sync.RWMutex
	settings ModelSettings
}

// NewModelRegistry creates a registry seeded with initial (defaults are applied
// to any blank field).
func NewModelRegistry(initial ModelSettings) *ModelRegistry {
	return &ModelRegistry{settings: initial.WithDefaults()}
}

// Get returns a snapshot of the current model selection.
func (r *ModelRegistry) Get() ModelSettings {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.settings
}

// Set validates and stores a new model selection.
func (r *ModelRegistry) Set(s ModelSettings) error {
	if err := s.Validate(); err != nil {
		return err
	}
	s = s.WithDefaults()
	r.mu.Lock()
	r.settings = s
	r.mu.Unlock()
	return nil
}

// SummaryModel returns the current summary model.
func (r *ModelRegistry) SummaryModel() string { return r.Get().SummaryModel }

// DossierModel returns the current dossier model.
func (r *ModelRegistry) DossierModel() string { return r.Get().DossierModel }

// VisionModel returns the current vision model.
func (r *ModelRegistry) VisionModel() string { return r.Get().VisionModel }

// EmbeddingModel returns the current embedding model.
func (r *ModelRegistry) EmbeddingModel() string { return r.Get().EmbeddingModel }
