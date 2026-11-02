package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ClientConfig holds configuration for the Push Alert Client.
type ClientConfig struct {
	AppriseURL     string
	NtfyURL        string
	ExternalWebURL string
	HTTPClient     *http.Client
}

// Client dispatches push notifications to ntfy and Apprise services.
type Client struct {
	appriseURL     string
	ntfyURL        string
	externalWebURL string
	httpClient     *http.Client
}

// NewClient initializes a new push notification Client.
func NewClient(cfg ClientConfig) *Client {
	extURL := strings.TrimRight(cfg.ExternalWebURL, "/")
	if extURL == "" {
		extURL = "http://localhost:8090"
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{
			Timeout: 10 * time.Second,
		}
	}

	return &Client{
		appriseURL:     cfg.AppriseURL,
		ntfyURL:        cfg.NtfyURL,
		externalWebURL: extURL,
		httpClient:     httpClient,
	}
}

// ApprisePayload represents the JSON payload structure accepted by Apprise.
type ApprisePayload struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	URL   string `json:"url,omitempty"`
}

// NtfyOptions configures headers and metadata for ntfy alerts.
type NtfyOptions struct {
	Title    string
	Priority string
	Tags     string
	Actions  string
	ClickURL string
}

// SendSessionExpiredAlert dispatches high-priority alert when session unlinks or tokens expire.
// Requirement ID: AUTH-03
func (c *Client) SendSessionExpiredAlert(ctx context.Context) error {
	title := "Google Messages Pairing Required"
	body := "Google Messages web session has expired or unpaired. Tap to re-pair on mobile."
	pairURL := c.externalWebURL + "/pair"

	ntfyOpts := NtfyOptions{
		Title:    title,
		Priority: "urgent", // 5
		Tags:     "warning,mobile_phone",
		Actions:  fmt.Sprintf("view, Re-pair Session, %s, clear=true", pairURL),
		ClickURL: pairURL,
	}

	apprisePayload := ApprisePayload{
		Title: title,
		Body:  body,
		URL:   pairURL,
	}

	return c.dispatch(ctx, title, body, ntfyOpts, apprisePayload)
}

// SendGaiaChallengeAlert dispatches high-priority alert for single-device Gaia emoji pairing.
// Requirement ID: AUTH-04
func (c *Client) SendGaiaChallengeAlert(ctx context.Context, emoji string) error {
	title := "Google Messages Pairing Challenge: " + emoji
	body := "Tap Google Messages on your phone and select: " + emoji
	gaiaURL := c.externalWebURL + "/pair/gaia"

	ntfyOpts := NtfyOptions{
		Title:    title,
		Priority: "urgent",
		Tags:     "key,mobile_phone",
		ClickURL: gaiaURL,
	}

	apprisePayload := ApprisePayload{
		Title: title,
		Body:  body,
		URL:   gaiaURL,
	}

	return c.dispatch(ctx, title, body, ntfyOpts, apprisePayload)
}

// SendPhoneDisconnectedAlert dispatches alert when phone disconnection exceeds grace period.
// Requirement ID: TRANS-04
func (c *Client) SendPhoneDisconnectedAlert(ctx context.Context, graceMinutes int) error {
	title := "Google Messages Phone Disconnected"
	body := fmt.Sprintf("Google Messages Phone Disconnected (%dm grace period elapsed)", graceMinutes)

	ntfyOpts := NtfyOptions{
		Title:    title,
		Priority: "high", // 4
		Tags:     "warning,phone",
	}

	apprisePayload := ApprisePayload{
		Title: title,
		Body:  body,
	}

	return c.dispatch(ctx, title, body, ntfyOpts, apprisePayload)
}

func (c *Client) dispatch(ctx context.Context, title, body string, ntfyOpts NtfyOptions, apprisePayload ApprisePayload) error {
	var errs []string

	if c.ntfyURL != "" {
		if err := c.sendNtfy(ctx, body, ntfyOpts); err != nil {
			errs = append(errs, fmt.Sprintf("ntfy error: %v", err))
		}
	}

	if c.appriseURL != "" {
		if err := c.sendApprise(ctx, apprisePayload); err != nil {
			errs = append(errs, fmt.Sprintf("apprise error: %v", err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("push dispatch failed: %s", strings.Join(errs, "; "))
	}
	return nil
}

func (c *Client) sendNtfy(ctx context.Context, body string, opts NtfyOptions) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.ntfyURL, strings.NewReader(body))
	if err != nil {
		return fmt.Errorf("create ntfy request: %w", err)
	}

	if opts.Title != "" {
		req.Header.Set("Title", opts.Title)
	}
	if opts.Priority != "" {
		req.Header.Set("Priority", opts.Priority)
	}
	if opts.Tags != "" {
		req.Header.Set("Tags", opts.Tags)
	}
	if opts.Actions != "" {
		req.Header.Set("Actions", opts.Actions)
	}
	if opts.ClickURL != "" {
		req.Header.Set("Click", opts.ClickURL)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("dispatch ntfy post: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("ntfy responded with HTTP %d", resp.StatusCode)
	}
	return nil
}

func (c *Client) sendApprise(ctx context.Context, payload ApprisePayload) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal apprise payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.appriseURL, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("create apprise request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("dispatch apprise post: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("apprise responded with HTTP %d", resp.StatusCode)
	}
	return nil
}
