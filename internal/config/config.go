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
	PauseIngestion              bool
	EnableMCPSendMessage        bool
	MCPAccessToken              string
	WebhookURL                  string
	WebhookAuthToken            string
	WebhookHMACSecret           string
	AppriseURL                  string
	NtfyURL                     string
	ExternalWebURL              string
	NotifyOnPhoneDisconnect     bool
	PhoneDisconnectGraceMinutes int
	PullFullSizeMedia           bool
	MediaDir                    string
	DossierDir                  string
	SyncMode                    string
	SyncIntervalMinutes         int
	SyncWindowMinutes           int
	PingIntervalMinutes         int
	AlertTimeoutCount           int
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
	pauseIngestion := getEnvBool("PAUSE_INGESTION", false)
	enableSend := getEnvBool("ENABLE_MCP_SEND_MESSAGE", false)
	notifyOnDisconnect := getEnvBool("NOTIFY_ON_PHONE_DISCONNECT", false)
	phoneGraceMinutes := getEnvInt("PHONE_DISCONNECT_GRACE_MINUTES", 15)
	pullFullSizeMedia := getEnvBool("PULL_FULL_SIZE_MEDIA", false)
	mediaDir := getEnv("MEDIA_DIR", "/data/media")
	dossierDir := getEnv("DOSSIER_DIR", "/data/dossiers")
	syncMode := strings.ToLower(getEnv("SYNC_MODE", "continuous"))
	if syncMode != "periodic" {
		syncMode = "continuous"
	}
	syncIntervalMinutes := getEnvInt("SYNC_INTERVAL_MINUTES", 30)
	if syncIntervalMinutes < 1 {
		syncIntervalMinutes = 30
	}
	syncWindowMinutes := getEnvInt("SYNC_WINDOW_MINUTES", 5)
	if syncWindowMinutes < 0 {
		syncWindowMinutes = 0
	}
	pingIntervalMinutes := getEnvInt("GM_PING_INTERVAL_MINUTES", 5)
	if pingIntervalMinutes < 1 {
		pingIntervalMinutes = 1
	}
	alertTimeoutCount := getEnvInt("GM_ALERT_TIMEOUT_COUNT", 2)
	if alertTimeoutCount < 1 {
		alertTimeoutCount = 1
	}

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
		PauseIngestion:              pauseIngestion,
		EnableMCPSendMessage:        enableSend,
		MCPAccessToken:              getEnv("MCP_ACCESS_TOKEN", ""),
		WebhookURL:                  getEnv("WEBHOOK_URL", ""),
		WebhookAuthToken:            getEnv("WEBHOOK_AUTH_TOKEN", ""),
		WebhookHMACSecret:           getEnv("WEBHOOK_HMAC_SECRET", ""),
		AppriseURL:                  getEnv("APPRISE_URL", ""),
		NtfyURL:                     getEnv("NTFY_URL", ""),
		ExternalWebURL:              getEnv("EXTERNAL_WEB_URL", "http://localhost:8090"),
		NotifyOnPhoneDisconnect:     notifyOnDisconnect,
		PhoneDisconnectGraceMinutes: phoneGraceMinutes,
		PullFullSizeMedia:           pullFullSizeMedia,
		MediaDir:                    mediaDir,
		DossierDir:                  dossierDir,
		SyncMode:                    syncMode,
		SyncIntervalMinutes:         syncIntervalMinutes,
		SyncWindowMinutes:           syncWindowMinutes,
		PingIntervalMinutes:         pingIntervalMinutes,
		AlertTimeoutCount:           alertTimeoutCount,
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
