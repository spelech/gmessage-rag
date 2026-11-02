package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// Config encapsulates runtime configuration settings for gmessages-rag.
type Config struct {
	DatabaseURL                 string
	HTTPPort                    int
	Timezone                    string
	Location                    *time.Location
	LLMBaseURL                  string
	LLMAPIKey                   string
	DossierModel                string
	SummaryModel                string
	VisionModel                 string
	EmbeddingBaseURL            string
	EmbeddingAPIKey             string
	EmbeddingModel              string
	EmbeddingDimensions         int
	TrackedContacts             []string
	SessionGapHours             int
	DossierDebounceMinutes      int
	MaxBackfillMessages         int
	EnableMCPSendMessage        bool
	MCPAccessToken              string
	WebhookURL                  string
	AppriseURL                  string
	NtfyURL                     string
	ExternalWebURL              string
	NotifyOnPhoneDisconnect     bool
	PhoneDisconnectGraceMinutes int
	PullFullSizeMedia           bool
	MediaDir                    string
}

// LoadConfig reads configuration from environment variables with fallback defaults.
func LoadConfig() (*Config, error) {
	tzStr := getEnv("TZ", "America/Chicago")
	loc, err := time.LoadLocation(tzStr)
	if err != nil {
		loc = time.UTC
	}

	httpPort := getEnvInt("HTTP_PORT", 8090)
	embeddingDims := getEnvInt("EMBEDDING_DIMENSIONS", 1536)
	sessionGapHours := getEnvInt("SESSION_GAP_HOURS", 2)
	debounceMins := getEnvInt("DOSSIER_DEBOUNCE_MINUTES", 30)
	maxBackfill := getEnvInt("MAX_BACKFILL_MESSAGES", 5000)
	enableSend := getEnvBool("ENABLE_MCP_SEND_MESSAGE", false)
	notifyOnDisconnect := getEnvBool("NOTIFY_ON_PHONE_DISCONNECT", false)
	phoneGraceMinutes := getEnvInt("PHONE_DISCONNECT_GRACE_MINUTES", 15)
	pullFullSizeMedia := getEnvBool("PULL_FULL_SIZE_MEDIA", false)
	mediaDir := getEnv("MEDIA_DIR", "/data/media")

	trackedRaw := getEnv("TRACKED_CONTACTS", "")
	var trackedContacts []string
	if trackedRaw != "" {
		parts := strings.Split(trackedRaw, ",")
		for _, p := range parts {
			trimmed := strings.TrimSpace(p)
			if trimmed != "" {
				trackedContacts = append(trackedContacts, trimmed)
			}
		}
	}

	cfg := &Config{
		DatabaseURL:                 getEnv("DATABASE_URL", "postgres://rcsrag:changeme@localhost:5432/gmessages?sslmode=disable"),
		HTTPPort:                    httpPort,
		Timezone:                    tzStr,
		Location:                    loc,
		LLMBaseURL:                  getEnv("LLM_BASE_URL", "https://openrouter.ai/api/v1"),
		LLMAPIKey:                   getEnv("LLM_API_KEY", ""),
		DossierModel:                getEnv("DOSSIER_MODEL", "anthropic/claude-3.7-sonnet"),
		SummaryModel:                getEnv("SUMMARY_MODEL", "google/gemini-2.5-flash"),
		VisionModel:                 getEnv("VISION_MODEL", "google/gemini-2.5-flash"),
		EmbeddingBaseURL:            getEnv("EMBEDDING_BASE_URL", "https://api.openai.com/v1"),
		EmbeddingAPIKey:             getEnv("EMBEDDING_API_KEY", ""),
		EmbeddingModel:              getEnv("EMBEDDING_MODEL", "text-embedding-3-small"),
		EmbeddingDimensions:         embeddingDims,
		TrackedContacts:             trackedContacts,
		SessionGapHours:             sessionGapHours,
		DossierDebounceMinutes:      debounceMins,
		MaxBackfillMessages:         maxBackfill,
		EnableMCPSendMessage:        enableSend,
		MCPAccessToken:              getEnv("MCP_ACCESS_TOKEN", ""),
		WebhookURL:                  getEnv("WEBHOOK_URL", ""),
		AppriseURL:                  getEnv("APPRISE_URL", ""),
		NtfyURL:                     getEnv("NTFY_URL", ""),
		ExternalWebURL:              getEnv("EXTERNAL_WEB_URL", "http://localhost:8090"),
		NotifyOnPhoneDisconnect:     notifyOnDisconnect,
		PhoneDisconnectGraceMinutes: phoneGraceMinutes,
		PullFullSizeMedia:           pullFullSizeMedia,
		MediaDir:                    mediaDir,
	}

	return cfg, nil
}

// IsTracked returns true if the contact name or identifier matches TRACKED_CONTACTS.
func (c *Config) IsTracked(identifier string) bool {
	if len(c.TrackedContacts) == 0 {
		return false
	}
	normalized := strings.ToLower(strings.TrimSpace(identifier))
	for _, tracked := range c.TrackedContacts {
		if strings.ToLower(strings.TrimSpace(tracked)) == normalized {
			return true
		}
	}
	return false
}

func getEnv(key, fallback string) string {
	if val, ok := os.LookupEnv(key); ok && strings.TrimSpace(val) != "" {
		return strings.TrimSpace(val)
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if val, ok := os.LookupEnv(key); ok {
		if parsed, err := strconv.Atoi(strings.TrimSpace(val)); err == nil {
			return parsed
		}
	}
	return fallback
}

func getEnvBool(key string, fallback bool) bool {
	if val, ok := os.LookupEnv(key); ok {
		if parsed, err := strconv.ParseBool(strings.TrimSpace(val)); err == nil {
			return parsed
		}
	}
	return fallback
}
