package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	defaultWebhookMaxAttempts = 5
	defaultWebhookBaseBackoff = 250 * time.Millisecond
	maxWebhookBackoff         = 30 * time.Second
)

// WebhookConfig configures the outbound dossier webhook dispatcher (TRANS-03).
type WebhookConfig struct {
	// URL is the delivery target. An empty URL disables dispatch.
	URL string
	// BearerToken, when set, is sent as "Authorization: Bearer <token>".
	BearerToken string
	// HMACSecret, when set, signs the body with HMAC-SHA256 into X-Webhook-Signature.
	HMACSecret string
	// HTTPClient overrides the default client (used by tests for a stub transport).
	HTTPClient *http.Client
	// MaxAttempts bounds the delivery attempts (including the first). Defaults to 5.
	MaxAttempts int
	// BaseBackoff is the first retry delay; it doubles per attempt. Defaults to 250ms.
	BaseBackoff time.Duration
}

// DossierWebhookEvent is the JSON payload delivered when a living dossier changes.
type DossierWebhookEvent struct {
	Event           string         `json:"event"`
	ConversationID  string         `json:"conversation_id"`
	ContactName     string         `json:"contact_name"`
	Version         int            `json:"version"`
	ChangedAt       time.Time      `json:"changed_at"`
	StructuredState map[string]any `json:"structured_state"`
	Markdown        string         `json:"markdown"`
}

// WebhookDispatcher delivers dossier update events to a configured HTTP endpoint with
// bounded exponential backoff. It never logs or embeds configured secrets in errors.
type WebhookDispatcher struct {
	url         string
	bearerToken string
	hmacSecret  string
	httpClient  *http.Client
	maxAttempts int
	baseBackoff time.Duration
}

// NewWebhookDispatcher constructs a WebhookDispatcher from cfg.
func NewWebhookDispatcher(cfg WebhookConfig) *WebhookDispatcher {
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}

	maxAttempts := cfg.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = defaultWebhookMaxAttempts
	}

	baseBackoff := cfg.BaseBackoff
	if baseBackoff <= 0 {
		baseBackoff = defaultWebhookBaseBackoff
	}

	return &WebhookDispatcher{
		url:         strings.TrimSpace(cfg.URL),
		bearerToken: cfg.BearerToken,
		hmacSecret:  cfg.HMACSecret,
		httpClient:  httpClient,
		maxAttempts: maxAttempts,
		baseBackoff: baseBackoff,
	}
}

// Enabled reports whether a delivery target is configured.
func (d *WebhookDispatcher) Enabled() bool {
	return d != nil && d.url != ""
}

// DispatchDossierUpdate delivers event to the configured URL, retrying with exponential backoff
// until success, exhaustion, or context cancellation.
// Requirement ID: TRANS-03
func (d *WebhookDispatcher) DispatchDossierUpdate(ctx context.Context, event DossierWebhookEvent) error {
	if !d.Enabled() {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal dossier webhook payload: %w", err)
	}

	var lastErr error
	for attempt := 1; attempt <= d.maxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("dossier webhook dispatch canceled: %w", err)
		}

		lastErr = d.send(ctx, payload)
		if lastErr == nil {
			return nil
		}
		if attempt == d.maxAttempts {
			break
		}
		if err := d.waitBackoff(ctx, attempt); err != nil {
			return fmt.Errorf("dossier webhook dispatch canceled: %w", err)
		}
	}

	return fmt.Errorf("dossier webhook dispatch failed after %d attempts: %w", d.maxAttempts, lastErr)
}

// send performs a single delivery attempt and returns a sanitized error on failure.
func (d *WebhookDispatcher) send(ctx context.Context, payload []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.url, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create dossier webhook request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "gmessages-rag-webhook/1.0")
	if d.bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+d.bearerToken)
	}
	if d.hmacSecret != "" {
		mac := hmac.New(sha256.New, []byte(d.hmacSecret))
		mac.Write(payload)
		req.Header.Set("X-Webhook-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	}

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("dossier webhook transport error: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("dossier webhook responded with HTTP %d", resp.StatusCode)
	}
	return nil
}

// waitBackoff sleeps for the exponential delay preceding the next attempt, honoring cancellation.
func (d *WebhookDispatcher) waitBackoff(ctx context.Context, attempt int) error {
	backoff := d.baseBackoff
	for i := 1; i < attempt; i++ {
		if backoff >= maxWebhookBackoff {
			backoff = maxWebhookBackoff
			break
		}
		backoff *= 2
	}

	timer := time.NewTimer(backoff)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
