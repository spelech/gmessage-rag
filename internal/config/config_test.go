package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/spelech/gmessage-rag/internal/config"
)

// TestRequirementAnnotations ensures requirement traceability:
// DB-01 (Database configuration validation)
// TRANS-01 (HTTP port and service endpoints configuration validation)

func setEnv(t *testing.T, key, val string) {
	t.Helper()
	orig, exists := os.LookupEnv(key)
	if err := os.Setenv(key, val); err != nil {
		t.Fatalf("failed to set env %s: %v", key, err)
	}
	t.Cleanup(func() {
		if exists {
			_ = os.Setenv(key, orig)
		} else {
			_ = os.Unsetenv(key)
		}
	})
}

func unsetAllConfigEnvs(t *testing.T) {
	t.Helper()
	keys := []string{
		"DATABASE_URL",
		"HTTP_PORT",
		"LLM_BASE_URL",
		"LLM_API_KEY",
		"DOSSIER_MODEL",
		"SUMMARY_MODEL",
		"VISION_MODEL",
		"EMBEDDING_BASE_URL",
		"EMBEDDING_API_KEY",
		"EMBEDDING_MODEL",
		"EMBEDDING_DIMENSIONS",
		"TRACKED_CONTACTS",
		"SESSION_GAP_HOURS",
		"DOSSIER_DEBOUNCE_MINUTES",
		"MAX_BACKFILL_MESSAGES",
		"ENABLE_MCP_SEND_MESSAGE",
		"WEBHOOK_URL",
		"CONFIG_PATH",
	}
	for _, k := range keys {
		orig, exists := os.LookupEnv(k)
		_ = os.Unsetenv(k)
		if exists {
			key := k
			val := orig
			t.Cleanup(func() {
				_ = os.Setenv(key, val)
			})
		}
	}
}

// TestConfig_NominalPathWithFullEnvironmentOverride exercises nominal loading with all env vars populated.
func TestConfig_NominalPathWithFullEnvironmentOverride(t *testing.T) {
	unsetAllConfigEnvs(t)

	setEnv(t, "DATABASE_URL", "postgres://user:pass@localhost:5432/testdb?sslmode=disable")
	setEnv(t, "HTTP_PORT", "9090")
	setEnv(t, "LLM_BASE_URL", "https://api.openai.com/v1")
	setEnv(t, "LLM_API_KEY", "sk-secret-llm")
	setEnv(t, "DOSSIER_MODEL", "gpt-4o")
	setEnv(t, "SUMMARY_MODEL", "gpt-4o-mini")
	setEnv(t, "VISION_MODEL", "gpt-4o")
	setEnv(t, "EMBEDDING_BASE_URL", "https://api.openai.com/v1")
	setEnv(t, "EMBEDDING_API_KEY", "sk-secret-emb")
	setEnv(t, "EMBEDDING_MODEL", "text-embedding-3-small")
	setEnv(t, "EMBEDDING_DIMENSIONS", "3072")
	setEnv(t, "TRACKED_CONTACTS", "Alice, Bob, +15551234567")
	setEnv(t, "SESSION_GAP_HOURS", "4")
	setEnv(t, "DOSSIER_DEBOUNCE_MINUTES", "45")
	setEnv(t, "MAX_BACKFILL_MESSAGES", "10000")
	setEnv(t, "ENABLE_MCP_SEND_MESSAGE", "true")
	setEnv(t, "WEBHOOK_URL", "https://homeassistant.local/api/webhook/test")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("unexpected error loading full config: %v", err)
	}

	if cfg.DatabaseURL != "postgres://user:pass@localhost:5432/testdb?sslmode=disable" {
		t.Errorf("DatabaseURL mismatch: got %v", cfg.DatabaseURL)
	}
	if cfg.HTTPPort != 9090 {
		t.Errorf("HTTPPort mismatch: got %v, want 9090", cfg.HTTPPort)
	}
	if cfg.LLMBaseURL != "https://api.openai.com/v1" {
		t.Errorf("LLMBaseURL mismatch: got %v", cfg.LLMBaseURL)
	}
	if cfg.LLMAPIKey != "sk-secret-llm" {
		t.Errorf("LLMAPIKey mismatch: got %v", cfg.LLMAPIKey)
	}
	if cfg.DossierModel != "gpt-4o" {
		t.Errorf("DossierModel mismatch: got %v", cfg.DossierModel)
	}
	if cfg.SummaryModel != "gpt-4o-mini" {
		t.Errorf("SummaryModel mismatch: got %v", cfg.SummaryModel)
	}
	if cfg.VisionModel != "gpt-4o" {
		t.Errorf("VisionModel mismatch: got %v", cfg.VisionModel)
	}
	if cfg.EmbeddingBaseURL != "https://api.openai.com/v1" {
		t.Errorf("EmbeddingBaseURL mismatch: got %v", cfg.EmbeddingBaseURL)
	}
	if cfg.EmbeddingAPIKey != "sk-secret-emb" {
		t.Errorf("EmbeddingAPIKey mismatch: got %v", cfg.EmbeddingAPIKey)
	}
	if cfg.EmbeddingModel != "text-embedding-3-small" {
		t.Errorf("EmbeddingModel mismatch: got %v", cfg.EmbeddingModel)
	}
	if cfg.EmbeddingDimensions != 3072 {
		t.Errorf("EmbeddingDimensions mismatch: got %v, want 3072", cfg.EmbeddingDimensions)
	}
	expectedContacts := []string{"Alice", "Bob", "+15551234567"}
	if !reflect.DeepEqual(cfg.TrackedContacts, expectedContacts) {
		t.Errorf("TrackedContacts mismatch: got %v, want %v", cfg.TrackedContacts, expectedContacts)
	}
	if cfg.SessionGapHours != 4 {
		t.Errorf("SessionGapHours mismatch: got %v, want 4", cfg.SessionGapHours)
	}
	if cfg.DossierDebounceMinutes != 45 {
		t.Errorf("DossierDebounceMinutes mismatch: got %v, want 45", cfg.DossierDebounceMinutes)
	}
	if cfg.MaxBackfillMessages != 10000 {
		t.Errorf("MaxBackfillMessages mismatch: got %v, want 10000", cfg.MaxBackfillMessages)
	}
	if !cfg.EnableMCPSendMessage {
		t.Errorf("EnableMCPSendMessage mismatch: got %v, want true", cfg.EnableMCPSendMessage)
	}
	if cfg.WebhookURL != "https://homeassistant.local/api/webhook/test" {
		t.Errorf("WebhookURL mismatch: got %v", cfg.WebhookURL)
	}
}

// TestConfig_DefaultsWhenEnvUnset verifies fallback to sane defaults.
func TestConfig_DefaultsWhenEnvUnset(t *testing.T) {
	unsetAllConfigEnvs(t)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("unexpected error loading default config: %v", err)
	}

	if cfg.HTTPPort != config.DefaultHTTPPort {
		t.Errorf("HTTPPort default mismatch: got %v, want %v", cfg.HTTPPort, config.DefaultHTTPPort)
	}
	if cfg.EmbeddingDimensions != config.DefaultEmbeddingDimensions {
		t.Errorf("EmbeddingDimensions default mismatch: got %v, want %v", cfg.EmbeddingDimensions, config.DefaultEmbeddingDimensions)
	}
	if cfg.SessionGapHours != config.DefaultSessionGapHours {
		t.Errorf("SessionGapHours default mismatch: got %v, want %v", cfg.SessionGapHours, config.DefaultSessionGapHours)
	}
	if cfg.DossierDebounceMinutes != config.DefaultDossierDebounceMinutes {
		t.Errorf("DossierDebounceMinutes default mismatch: got %v, want %v", cfg.DossierDebounceMinutes, config.DefaultDossierDebounceMinutes)
	}
	if cfg.MaxBackfillMessages != config.DefaultMaxBackfillMessages {
		t.Errorf("MaxBackfillMessages default mismatch: got %v, want %v", cfg.MaxBackfillMessages, config.DefaultMaxBackfillMessages)
	}
	if cfg.EnableMCPSendMessage != config.DefaultEnableMCPSendMessage {
		t.Errorf("EnableMCPSendMessage default mismatch: got %v, want %v", cfg.EnableMCPSendMessage, config.DefaultEnableMCPSendMessage)
	}
	if len(cfg.TrackedContacts) != 0 {
		t.Errorf("TrackedContacts default mismatch: got %v, want empty", cfg.TrackedContacts)
	}
}

// TestConfig_YAMLAndEnvPrecedence checks YAML loading with environment variable overrides.
func TestConfig_YAMLAndEnvPrecedence(t *testing.T) {
	unsetAllConfigEnvs(t)

	tmpDir := t.TempDir()
	yamlFile := filepath.Join(tmpDir, "config.yaml")

	yamlContent := `
http_port: 8080
database_url: "postgres://yamluser:yamlpass@localhost:5432/yamldb?sslmode=disable"
embedding_dimensions: 768
tracked_contacts:
  - "ContactFromYAML1"
  - "ContactFromYAML2"
session_gap_hours: 3
dossier_debounce_minutes: 15
max_backfill_messages: 2000
enable_mcp_send_message: true
webhook_url: "https://yaml-webhook.local"
`
	if err := os.WriteFile(yamlFile, []byte(yamlContent), 0600); err != nil {
		t.Fatalf("failed to write temp yaml file: %v", err)
	}

	// Environment variable should override YAML
	setEnv(t, "HTTP_PORT", "9999")
	setEnv(t, "TRACKED_CONTACTS", "OverrideContact")

	cfg, err := config.Load(yamlFile)
	if err != nil {
		t.Fatalf("failed to load config with YAML: %v", err)
	}

	// Overridden by env
	if cfg.HTTPPort != 9999 {
		t.Errorf("HTTPPort should be overridden by env: got %v, want 9999", cfg.HTTPPort)
	}
	if !reflect.DeepEqual(cfg.TrackedContacts, []string{"OverrideContact"}) {
		t.Errorf("TrackedContacts should be overridden by env: got %v", cfg.TrackedContacts)
	}

	// Kept from YAML
	if cfg.DatabaseURL != "postgres://yamluser:yamlpass@localhost:5432/yamldb?sslmode=disable" {
		t.Errorf("DatabaseURL from YAML mismatch: got %v", cfg.DatabaseURL)
	}
	if cfg.EmbeddingDimensions != 768 {
		t.Errorf("EmbeddingDimensions from YAML mismatch: got %v, want 768", cfg.EmbeddingDimensions)
	}
	if cfg.SessionGapHours != 3 {
		t.Errorf("SessionGapHours from YAML mismatch: got %v, want 3", cfg.SessionGapHours)
	}
	if cfg.DossierDebounceMinutes != 15 {
		t.Errorf("DossierDebounceMinutes from YAML mismatch: got %v, want 15", cfg.DossierDebounceMinutes)
	}
	if cfg.MaxBackfillMessages != 2000 {
		t.Errorf("MaxBackfillMessages from YAML mismatch: got %v, want 2000", cfg.MaxBackfillMessages)
	}
	if !cfg.EnableMCPSendMessage {
		t.Errorf("EnableMCPSendMessage from YAML mismatch: got %v, want true", cfg.EnableMCPSendMessage)
	}
	if cfg.WebhookURL != "https://yaml-webhook.local" {
		t.Errorf("WebhookURL from YAML mismatch: got %v", cfg.WebhookURL)
	}
}

// TestConfig_TrackedContacts_0_1_N_Boundaries validates 0-1-N boundary sweeps.
func TestConfig_TrackedContacts_0_1_N_Boundaries(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "0: Empty string",
			input:    "",
			expected: []string{},
		},
		{
			name:     "0: Whitespace only string",
			input:    "   ,   ,  ",
			expected: []string{},
		},
		{
			name:     "1: Single contact",
			input:    "Sarah",
			expected: []string{"Sarah"},
		},
		{
			name:     "1: Single contact with surrounding whitespace",
			input:    "  +15551234567   ",
			expected: []string{"+15551234567"},
		},
		{
			name:     "N: Multiple contacts comma-separated",
			input:    "Sarah, John Doe, +1 (555) 987-6543",
			expected: []string{"Sarah", "John Doe", "+1 (555) 987-6543"},
		},
		{
			name:     "N: Multiple contacts with empty slots and extra spaces",
			input:    " Alice ,, Bob ,   , Charlie ",
			expected: []string{"Alice", "Bob", "Charlie"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := config.ParseTrackedContacts(tc.input)
			if !reflect.DeepEqual(got, tc.expected) {
				t.Errorf("ParseTrackedContacts(%q) = %v; want %v", tc.input, got, tc.expected)
			}
		})
	}
}

// TestConfig_DisturbancesAndFailClosed validates that malformed integer/boolean/URL inputs fail closed with typed errors.
func TestConfig_DisturbancesAndFailClosed(t *testing.T) {
	cases := []struct {
		name        string
		envKey      string
		envVal      string
		expectField string
	}{
		{
			name:        "Malformed HTTP_PORT non-integer",
			envKey:      "HTTP_PORT",
			envVal:      "not-a-port",
			expectField: "HTTP_PORT",
		},
		{
			name:        "Out of bounds HTTP_PORT zero",
			envKey:      "HTTP_PORT",
			envVal:      "0",
			expectField: "HTTP_PORT",
		},
		{
			name:        "Out of bounds HTTP_PORT too large",
			envKey:      "HTTP_PORT",
			envVal:      "70000",
			expectField: "HTTP_PORT",
		},
		{
			name:        "Malformed EMBEDDING_DIMENSIONS string",
			envKey:      "EMBEDDING_DIMENSIONS",
			envVal:      "abc",
			expectField: "EMBEDDING_DIMENSIONS",
		},
		{
			name:        "Invalid EMBEDDING_DIMENSIONS zero",
			envKey:      "EMBEDDING_DIMENSIONS",
			envVal:      "0",
			expectField: "EMBEDDING_DIMENSIONS",
		},
		{
			name:        "Malformed SESSION_GAP_HOURS non-integer",
			envKey:      "SESSION_GAP_HOURS",
			envVal:      "two",
			expectField: "SESSION_GAP_HOURS",
		},
		{
			name:        "Invalid SESSION_GAP_HOURS zero",
			envKey:      "SESSION_GAP_HOURS",
			envVal:      "0",
			expectField: "SESSION_GAP_HOURS",
		},
		{
			name:        "Malformed DOSSIER_DEBOUNCE_MINUTES non-integer",
			envKey:      "DOSSIER_DEBOUNCE_MINUTES",
			envVal:      "invalid_mins",
			expectField: "DOSSIER_DEBOUNCE_MINUTES",
		},
		{
			name:        "Invalid DOSSIER_DEBOUNCE_MINUTES zero",
			envKey:      "DOSSIER_DEBOUNCE_MINUTES",
			envVal:      "0",
			expectField: "DOSSIER_DEBOUNCE_MINUTES",
		},
		{
			name:        "Malformed MAX_BACKFILL_MESSAGES non-integer",
			envKey:      "MAX_BACKFILL_MESSAGES",
			envVal:      "five_thousand",
			expectField: "MAX_BACKFILL_MESSAGES",
		},
		{
			name:        "Invalid MAX_BACKFILL_MESSAGES negative",
			envKey:      "MAX_BACKFILL_MESSAGES",
			envVal:      "-10",
			expectField: "MAX_BACKFILL_MESSAGES",
		},
		{
			name:        "Malformed ENABLE_MCP_SEND_MESSAGE non-boolean",
			envKey:      "ENABLE_MCP_SEND_MESSAGE",
			envVal:      "yes_please",
			expectField: "ENABLE_MCP_SEND_MESSAGE",
		},
		{
			name:        "Malformed LLM_BASE_URL invalid scheme",
			envKey:      "LLM_BASE_URL",
			envVal:      "ftp://api.example.com",
			expectField: "LLM_BASE_URL",
		},
		{
			name:        "Malformed EMBEDDING_BASE_URL invalid scheme",
			envKey:      "EMBEDDING_BASE_URL",
			envVal:      "invalid-url",
			expectField: "EMBEDDING_BASE_URL",
		},
		{
			name:        "Malformed WEBHOOK_URL invalid scheme",
			envKey:      "WEBHOOK_URL",
			envVal:      "://missing-scheme",
			expectField: "WEBHOOK_URL",
		},
		{
			name:        "Malformed DATABASE_URL",
			envKey:      "DATABASE_URL",
			envVal:      "redis://localhost:6379",
			expectField: "DATABASE_URL",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			unsetAllConfigEnvs(t)
			setEnv(t, tc.envKey, tc.envVal)

			cfg, err := config.Load()
			if err == nil {
				t.Fatalf("expected error for %s=%q, but got config: %+v", tc.envKey, tc.envVal, cfg)
			}

			if !config.IsValidationError(err) {
				t.Errorf("expected ValidationError, got %T: %v", err, err)
			}

			var valErr *config.ValidationError
			if errors.As(err, &valErr) {
				found := false
				for _, fe := range valErr.Errors {
					if fe.Field == tc.expectField {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected validation error on field %s, got errors: %v", tc.expectField, valErr.Errors)
				}
			}
		})
	}
}

// TestConfig_InvalidYAML checks fail-closed behavior on corrupted YAML file.
func TestConfig_InvalidYAML(t *testing.T) {
	unsetAllConfigEnvs(t)

	tmpDir := t.TempDir()
	invalidFile := filepath.Join(tmpDir, "broken.yaml")
	if err := os.WriteFile(invalidFile, []byte("http_port: [this is not an integer"), 0600); err != nil {
		t.Fatalf("failed to write invalid yaml: %v", err)
	}

	_, err := config.Load(invalidFile)
	if err == nil {
		t.Fatal("expected error on malformed YAML, got nil")
	}
}

// TestConfig_DSNDatabaseURL verifies key=value format PostgreSQL connection string is accepted.
func TestConfig_DSNDatabaseURL(t *testing.T) {
	unsetAllConfigEnvs(t)

	setEnv(t, "DATABASE_URL", "host=localhost port=5432 user=rcsrag dbname=gmessages sslmode=disable")
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("unexpected error for DSN DATABASE_URL: %v", err)
	}
	if cfg.DatabaseURL != "host=localhost port=5432 user=rcsrag dbname=gmessages sslmode=disable" {
		t.Errorf("DatabaseURL mismatch: got %v", cfg.DatabaseURL)
	}
}
