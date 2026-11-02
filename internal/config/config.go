package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Default configuration constants
const (
	DefaultHTTPPort               = 8090
	DefaultEmbeddingDimensions    = 1536
	DefaultSessionGapHours        = 2
	DefaultDossierDebounceMinutes = 30
	DefaultMaxBackfillMessages    = 5000
	DefaultEnableMCPSendMessage   = false
)

// ValidationError represents one or more configuration validation failures.
type ValidationError struct {
	Errors []FieldError
}

// FieldError details a specific field validation failure.
type FieldError struct {
	Field   string
	Message string
	Value   any
}

func (e FieldError) Error() string {
	return fmt.Sprintf("invalid %s: %s (got: %v)", e.Field, e.Message, e.Value)
}

func (e *ValidationError) Error() string {
	if len(e.Errors) == 0 {
		return "validation error"
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%d configuration validation error(s): ", len(e.Errors)))
	for i, err := range e.Errors {
		if i > 0 {
			sb.WriteString("; ")
		}
		sb.WriteString(err.Error())
	}
	return sb.String()
}

// Config defines runtime settings for gmessages-rag.
type Config struct {
	DatabaseURL            string   `yaml:"database_url"`
	HTTPPort               int      `yaml:"http_port"`
	LLMBaseURL             string   `yaml:"llm_base_url"`
	LLMAPIKey              string   `yaml:"llm_api_key"`
	DossierModel           string   `yaml:"dossier_model"`
	SummaryModel           string   `yaml:"summary_model"`
	VisionModel            string   `yaml:"vision_model"`
	EmbeddingBaseURL       string   `yaml:"embedding_base_url"`
	EmbeddingAPIKey        string   `yaml:"embedding_api_key"`
	EmbeddingModel         string   `yaml:"embedding_model"`
	EmbeddingDimensions    int      `yaml:"embedding_dimensions"`
	TrackedContacts        []string `yaml:"tracked_contacts"`
	SessionGapHours        int      `yaml:"session_gap_hours"`
	DossierDebounceMinutes int      `yaml:"dossier_debounce_minutes"`
	MaxBackfillMessages    int      `yaml:"max_backfill_messages"`
	EnableMCPSendMessage   bool     `yaml:"enable_mcp_send_message"`
	WebhookURL             string   `yaml:"webhook_url"`
}

// NewDefaultConfig returns a Config struct initialized with default values.
func NewDefaultConfig() *Config {
	return &Config{
		HTTPPort:               DefaultHTTPPort,
		EmbeddingDimensions:    DefaultEmbeddingDimensions,
		TrackedContacts:        []string{},
		SessionGapHours:        DefaultSessionGapHours,
		DossierDebounceMinutes: DefaultDossierDebounceMinutes,
		MaxBackfillMessages:    DefaultMaxBackfillMessages,
		EnableMCPSendMessage:   DefaultEnableMCPSendMessage,
	}
}

// Load loads configuration with the following precedence:
// 1. Defaults
// 2. YAML file (if configPath is provided or CONFIG_PATH env is set and exists)
// 3. Environment variables (overrides YAML and defaults)
// 4. Validation
func Load(configPath ...string) (*Config, error) {
	cfg := NewDefaultConfig()

	var path string
	if len(configPath) > 0 && configPath[0] != "" {
		path = configPath[0]
	} else {
		path = os.Getenv("CONFIG_PATH")
	}

	if path != "" {
		if err := cfg.LoadYAML(path); err != nil {
			return nil, fmt.Errorf("failed to load YAML config from %s: %w", path, err)
		}
	}

	if err := cfg.LoadEnv(); err != nil {
		return nil, fmt.Errorf("failed to load environment config: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

// LoadYAML reads and unmarshals settings from a YAML file over the current config.
func (c *Config) LoadYAML(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return yaml.Unmarshal(data, c)
}

// LoadEnv reads environment variables and overrides configuration settings.
func (c *Config) LoadEnv() error {
	var errs []FieldError

	if v, ok := os.LookupEnv("DATABASE_URL"); ok {
		c.DatabaseURL = strings.TrimSpace(v)
	}

	if v, ok := os.LookupEnv("HTTP_PORT"); ok {
		v = strings.TrimSpace(v)
		if v != "" {
			port, err := strconv.Atoi(v)
			if err != nil {
				errs = append(errs, FieldError{Field: "HTTP_PORT", Message: "must be a valid integer", Value: v})
			} else {
				c.HTTPPort = port
			}
		}
	}

	if v, ok := os.LookupEnv("LLM_BASE_URL"); ok {
		c.LLMBaseURL = strings.TrimSpace(v)
	}
	if v, ok := os.LookupEnv("LLM_API_KEY"); ok {
		c.LLMAPIKey = strings.TrimSpace(v)
	}
	if v, ok := os.LookupEnv("DOSSIER_MODEL"); ok {
		c.DossierModel = strings.TrimSpace(v)
	}
	if v, ok := os.LookupEnv("SUMMARY_MODEL"); ok {
		c.SummaryModel = strings.TrimSpace(v)
	}
	if v, ok := os.LookupEnv("VISION_MODEL"); ok {
		c.VisionModel = strings.TrimSpace(v)
	}

	if v, ok := os.LookupEnv("EMBEDDING_BASE_URL"); ok {
		c.EmbeddingBaseURL = strings.TrimSpace(v)
	}
	if v, ok := os.LookupEnv("EMBEDDING_API_KEY"); ok {
		c.EmbeddingAPIKey = strings.TrimSpace(v)
	}
	if v, ok := os.LookupEnv("EMBEDDING_MODEL"); ok {
		c.EmbeddingModel = strings.TrimSpace(v)
	}

	if v, ok := os.LookupEnv("EMBEDDING_DIMENSIONS"); ok {
		v = strings.TrimSpace(v)
		if v != "" {
			dims, err := strconv.Atoi(v)
			if err != nil {
				errs = append(errs, FieldError{Field: "EMBEDDING_DIMENSIONS", Message: "must be a valid integer", Value: v})
			} else {
				c.EmbeddingDimensions = dims
			}
		}
	}

	if v, ok := os.LookupEnv("TRACKED_CONTACTS"); ok {
		c.TrackedContacts = ParseTrackedContacts(v)
	}

	if v, ok := os.LookupEnv("SESSION_GAP_HOURS"); ok {
		v = strings.TrimSpace(v)
		if v != "" {
			hours, err := strconv.Atoi(v)
			if err != nil {
				errs = append(errs, FieldError{Field: "SESSION_GAP_HOURS", Message: "must be a valid integer", Value: v})
			} else {
				c.SessionGapHours = hours
			}
		}
	}

	if v, ok := os.LookupEnv("DOSSIER_DEBOUNCE_MINUTES"); ok {
		v = strings.TrimSpace(v)
		if v != "" {
			mins, err := strconv.Atoi(v)
			if err != nil {
				errs = append(errs, FieldError{Field: "DOSSIER_DEBOUNCE_MINUTES", Message: "must be a valid integer", Value: v})
			} else {
				c.DossierDebounceMinutes = mins
			}
		}
	}

	if v, ok := os.LookupEnv("MAX_BACKFILL_MESSAGES"); ok {
		v = strings.TrimSpace(v)
		if v != "" {
			maxMsgs, err := strconv.Atoi(v)
			if err != nil {
				errs = append(errs, FieldError{Field: "MAX_BACKFILL_MESSAGES", Message: "must be a valid integer", Value: v})
			} else {
				c.MaxBackfillMessages = maxMsgs
			}
		}
	}

	if v, ok := os.LookupEnv("ENABLE_MCP_SEND_MESSAGE"); ok {
		v = strings.TrimSpace(v)
		if v != "" {
			enabled, err := strconv.ParseBool(v)
			if err != nil {
				errs = append(errs, FieldError{Field: "ENABLE_MCP_SEND_MESSAGE", Message: "must be a valid boolean", Value: v})
			} else {
				c.EnableMCPSendMessage = enabled
			}
		}
	}

	if v, ok := os.LookupEnv("WEBHOOK_URL"); ok {
		c.WebhookURL = strings.TrimSpace(v)
	}

	if len(errs) > 0 {
		return &ValidationError{Errors: errs}
	}

	return nil
}

// ParseTrackedContacts splits a comma-separated string, trimming spaces and discarding empty items.
func ParseTrackedContacts(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return []string{}
	}
	parts := strings.Split(raw, ",")
	contacts := make([]string, 0, len(parts))
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			contacts = append(contacts, trimmed)
		}
	}
	return contacts
}

// Validate verifies runtime constraints across the configuration fields.
func (c *Config) Validate() error {
	var errs []FieldError

	// Port check: 1 to 65535
	if c.HTTPPort < 1 || c.HTTPPort > 65535 {
		errs = append(errs, FieldError{
			Field:   "HTTP_PORT",
			Message: "must be between 1 and 65535",
			Value:   c.HTTPPort,
		})
	}

	// DatabaseURL check
	if c.DatabaseURL != "" {
		if !isValidDatabaseURL(c.DatabaseURL) {
			errs = append(errs, FieldError{
				Field:   "DATABASE_URL",
				Message: "must be a valid PostgreSQL connection string or URI",
				Value:   c.DatabaseURL,
			})
		}
	}

	// Embedding dimensions check
	if c.EmbeddingDimensions <= 0 {
		errs = append(errs, FieldError{
			Field:   "EMBEDDING_DIMENSIONS",
			Message: "must be greater than 0",
			Value:   c.EmbeddingDimensions,
		})
	}

	// Session gap hours check
	if c.SessionGapHours <= 0 {
		errs = append(errs, FieldError{
			Field:   "SESSION_GAP_HOURS",
			Message: "must be greater than 0",
			Value:   c.SessionGapHours,
		})
	}

	// Dossier debounce minutes check
	if c.DossierDebounceMinutes <= 0 {
		errs = append(errs, FieldError{
			Field:   "DOSSIER_DEBOUNCE_MINUTES",
			Message: "must be greater than 0",
			Value:   c.DossierDebounceMinutes,
		})
	}

	// Max backfill messages check
	if c.MaxBackfillMessages < 0 {
		errs = append(errs, FieldError{
			Field:   "MAX_BACKFILL_MESSAGES",
			Message: "must be non-negative",
			Value:   c.MaxBackfillMessages,
		})
	}

	// URLs validation if non-empty
	if c.LLMBaseURL != "" && !isValidHTTPURL(c.LLMBaseURL) {
		errs = append(errs, FieldError{
			Field:   "LLM_BASE_URL",
			Message: "must be a valid HTTP/HTTPS URL",
			Value:   c.LLMBaseURL,
		})
	}

	if c.EmbeddingBaseURL != "" && !isValidHTTPURL(c.EmbeddingBaseURL) {
		errs = append(errs, FieldError{
			Field:   "EMBEDDING_BASE_URL",
			Message: "must be a valid HTTP/HTTPS URL",
			Value:   c.EmbeddingBaseURL,
		})
	}

	if c.WebhookURL != "" && !isValidHTTPURL(c.WebhookURL) {
		errs = append(errs, FieldError{
			Field:   "WEBHOOK_URL",
			Message: "must be a valid HTTP/HTTPS URL",
			Value:   c.WebhookURL,
		})
	}

	if len(errs) > 0 {
		return &ValidationError{Errors: errs}
	}

	return nil
}

func isValidHTTPURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return u.Scheme == "http" || u.Scheme == "https"
}

func isValidDatabaseURL(raw string) bool {
	// Can be postgres:// or postgresql:// or key=value DSN
	if strings.HasPrefix(raw, "postgres://") || strings.HasPrefix(raw, "postgresql://") {
		u, err := url.Parse(raw)
		if err != nil {
			return false
		}
		return u.Host != ""
	}
	// Check DSN style (e.g. host=... dbname=...)
	if strings.Contains(raw, "=") {
		return true
	}
	return false
}

// IsValidationError checks whether an error is or contains a *ValidationError.
func IsValidationError(err error) bool {
	var valErr *ValidationError
	return errors.As(err, &valErr)
}
