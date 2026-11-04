package config_test

import (
	"os"
	"testing"

	"github.com/spelech/gmessage-rag/internal/config"
)

func TestLoadConfig_Defaults(t *testing.T) {
	// Ensure clean environment
	os.Clearenv()

	cfg, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("unexpected error loading defaults: %v", err)
	}

	if cfg.HTTPPort != 8090 {
		t.Errorf("expected default HTTPPort 8090, got %d", cfg.HTTPPort)
	}
	if cfg.EmbeddingDimensions != 1536 {
		t.Errorf("expected default EmbeddingDimensions 1536, got %d", cfg.EmbeddingDimensions)
	}
	if cfg.SessionGapHours != 2 {
		t.Errorf("expected default SessionGapHours 2, got %d", cfg.SessionGapHours)
	}
	if cfg.DossierDebounceMinutes != 30 {
		t.Errorf("expected default DossierDebounceMinutes 30, got %d", cfg.DossierDebounceMinutes)
	}
	if cfg.EnableMCPSendMessage {
		t.Errorf("expected EnableMCPSendMessage to be false by default")
	}
	if len(cfg.TrackedContacts) != 0 {
		t.Errorf("expected empty TrackedContacts by default, got %v", cfg.TrackedContacts)
	}
	if cfg.ExternalWebURL != "http://localhost:8090" {
		t.Errorf("expected default ExternalWebURL 'http://localhost:8090', got %s", cfg.ExternalWebURL)
	}
	if cfg.NotifyOnPhoneDisconnect {
		t.Errorf("expected NotifyOnPhoneDisconnect false by default")
	}
	if cfg.PhoneDisconnectGraceMinutes != 15 {
		t.Errorf("expected default PhoneDisconnectGraceMinutes 15, got %d", cfg.PhoneDisconnectGraceMinutes)
	}
	if cfg.PullFullSizeMedia {
		t.Errorf("expected default PullFullSizeMedia false, got true")
	}
	if cfg.MediaDir != "/data/media" {
		t.Errorf("expected default MediaDir '/data/media', got %s", cfg.MediaDir)
	}
}

func TestLoadConfig_OverridesAndBoundaries(t *testing.T) {
	os.Setenv("HTTP_PORT", "9090")
	os.Setenv("TRACKED_CONTACTS", "Sarah, +13125551212, Mom ")
	os.Setenv("ENABLE_MCP_SEND_MESSAGE", "true")
	os.Setenv("TZ", "America/New_York")
	os.Setenv("APPRISE_URL", "http://apprise:8000/notify")
	os.Setenv("NTFY_URL", "https://ntfy.sh/my-topic")
	os.Setenv("EXTERNAL_WEB_URL", "https://gmessages.home.arpa")
	os.Setenv("NOTIFY_ON_PHONE_DISCONNECT", "true")
	os.Setenv("PHONE_DISCONNECT_GRACE_MINUTES", "20")
	os.Setenv("PULL_FULL_SIZE_MEDIA", "true")
	os.Setenv("MEDIA_DIR", "/custom/media")
	defer os.Clearenv()

	cfg, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("unexpected error loading overrides: %v", err)
	}

	if cfg.HTTPPort != 9090 {
		t.Errorf("expected HTTPPort 9090, got %d", cfg.HTTPPort)
	}
	if !cfg.EnableMCPSendMessage {
		t.Errorf("expected EnableMCPSendMessage to be true")
	}
	if len(cfg.TrackedContacts) != 3 {
		t.Fatalf("expected 3 tracked contacts, got %d", len(cfg.TrackedContacts))
	}
	if !cfg.IsTracked("sarah") {
		t.Errorf("expected 'sarah' to be tracked (case-insensitive)")
	}
	if !cfg.IsTracked("+13125551212") {
		t.Errorf("expected '+13125551212' to be tracked")
	}
	if cfg.IsTracked("Unknown") {
		t.Errorf("expected 'Unknown' to not be tracked")
	}
	if cfg.AppriseURL != "http://apprise:8000/notify" {
		t.Errorf("expected AppriseURL 'http://apprise:8000/notify', got %s", cfg.AppriseURL)
	}
	if cfg.NtfyURL != "https://ntfy.sh/my-topic" {
		t.Errorf("expected NtfyURL 'https://ntfy.sh/my-topic', got %s", cfg.NtfyURL)
	}
	if cfg.ExternalWebURL != "https://gmessages.home.arpa" {
		t.Errorf("expected ExternalWebURL 'https://gmessages.home.arpa', got %s", cfg.ExternalWebURL)
	}
	if !cfg.NotifyOnPhoneDisconnect {
		t.Errorf("expected NotifyOnPhoneDisconnect true")
	}
	if cfg.PhoneDisconnectGraceMinutes != 20 {
		t.Errorf("expected PhoneDisconnectGraceMinutes 20, got %d", cfg.PhoneDisconnectGraceMinutes)
	}
	if !cfg.PullFullSizeMedia {
		t.Errorf("expected PullFullSizeMedia true")
	}
	if cfg.MediaDir != "/custom/media" {
		t.Errorf("expected MediaDir '/custom/media', got %s", cfg.MediaDir)
	}
}

// TestLoadConfig_SyncSettings verifies SYNC-01 configuration parsing: defaults,
// overrides, and boundary fallbacks for the connection schedule and ping tuning.
func TestLoadConfig_SyncSettings(t *testing.T) {
	os.Clearenv()
	cfg, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("unexpected error loading defaults: %v", err)
	}
	if cfg.SyncMode != "continuous" {
		t.Errorf("expected default SyncMode continuous, got %q", cfg.SyncMode)
	}
	if cfg.SyncIntervalMinutes != 30 {
		t.Errorf("expected default SyncIntervalMinutes 30, got %d", cfg.SyncIntervalMinutes)
	}
	if cfg.SyncWindowMinutes != 5 {
		t.Errorf("expected default SyncWindowMinutes 5, got %d", cfg.SyncWindowMinutes)
	}
	if cfg.PingIntervalMinutes != 5 {
		t.Errorf("expected default PingIntervalMinutes 5, got %d", cfg.PingIntervalMinutes)
	}
	if cfg.AlertTimeoutCount != 2 {
		t.Errorf("expected default AlertTimeoutCount 2, got %d", cfg.AlertTimeoutCount)
	}

	os.Setenv("SYNC_MODE", "periodic")
	os.Setenv("SYNC_INTERVAL_MINUTES", "15")
	os.Setenv("SYNC_WINDOW_MINUTES", "2")
	os.Setenv("GM_PING_INTERVAL_MINUTES", "3")
	os.Setenv("GM_ALERT_TIMEOUT_COUNT", "1")
	defer os.Clearenv()

	cfg, err = config.LoadConfig()
	if err != nil {
		t.Fatalf("unexpected error loading overrides: %v", err)
	}
	if cfg.SyncMode != "periodic" {
		t.Errorf("expected SyncMode periodic, got %q", cfg.SyncMode)
	}
	if cfg.SyncIntervalMinutes != 15 {
		t.Errorf("expected SyncIntervalMinutes 15, got %d", cfg.SyncIntervalMinutes)
	}
	if cfg.SyncWindowMinutes != 2 {
		t.Errorf("expected SyncWindowMinutes 2, got %d", cfg.SyncWindowMinutes)
	}
	if cfg.PingIntervalMinutes != 3 {
		t.Errorf("expected PingIntervalMinutes 3, got %d", cfg.PingIntervalMinutes)
	}
	if cfg.AlertTimeoutCount != 1 {
		t.Errorf("expected AlertTimeoutCount 1, got %d", cfg.AlertTimeoutCount)
	}

	// Unknown mode and sub-minute interval fall back to safe values.
	os.Setenv("SYNC_MODE", "turbo")
	os.Setenv("SYNC_INTERVAL_MINUTES", "0")
	cfg, err = config.LoadConfig()
	if err != nil {
		t.Fatalf("unexpected error loading fallbacks: %v", err)
	}
	if cfg.SyncMode != "continuous" {
		t.Errorf("expected unknown SyncMode to fall back to continuous, got %q", cfg.SyncMode)
	}
	if cfg.SyncIntervalMinutes != 30 {
		t.Errorf("expected 0 interval to fall back to 30, got %d", cfg.SyncIntervalMinutes)
	}
}
