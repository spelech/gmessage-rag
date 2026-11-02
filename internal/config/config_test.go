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
}
