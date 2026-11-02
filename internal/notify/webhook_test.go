package notify_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/spelech/gmessage-rag/internal/notify"
)

// Requirement ID: TRANS-03
// TestWebhookDispatch_RetriesOnFailure proves the dispatcher retries failed deliveries with
// backoff and stops once the stub endpoint accepts the payload.
func TestWebhookDispatch_RetriesOnFailure(t *testing.T) {
	var mu sync.Mutex
	deliveries := 0
	var receivedPayloads []notify.DossierWebhookEvent

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var event notify.DossierWebhookEvent
		_ = json.Unmarshal(body, &event)

		mu.Lock()
		deliveries++
		attempt := deliveries
		receivedPayloads = append(receivedPayloads, event)
		mu.Unlock()

		// Fail the first two attempts, succeed on the third.
		if attempt < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	dispatcher := notify.NewWebhookDispatcher(notify.WebhookConfig{
		URL:         server.URL,
		MaxAttempts: 4,
		BaseBackoff: time.Millisecond,
	})

	err := dispatcher.DispatchDossierUpdate(context.Background(), notify.DossierWebhookEvent{
		Event:           "dossier.updated",
		ConversationID:  "conv-retry-1",
		ContactName:     "Ada Lovelace",
		Version:         2,
		ChangedAt:       time.Now().UTC(),
		StructuredState: map[string]any{"role": "Analyst"},
		Markdown:        "# Dossier",
	})
	if err != nil {
		t.Fatalf("expected nil error after successful retry, got %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	if deliveries != 3 {
		t.Fatalf("expected 3 delivery attempts (2 failures + 1 success), got %d", deliveries)
	}
	if len(receivedPayloads) != 3 {
		t.Fatalf("expected the stub server to receive 3 deliveries, got %d", len(receivedPayloads))
	}
	for i, event := range receivedPayloads {
		if event.ConversationID != "conv-retry-1" || event.Version != 2 {
			t.Errorf("delivery %d carried unexpected payload: %+v", i, event)
		}
	}
}

// TestWebhookDispatch_ExhaustsAttempts proves the dispatcher gives up after MaxAttempts.
func TestWebhookDispatch_ExhaustsAttempts(t *testing.T) {
	var mu sync.Mutex
	deliveries := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		deliveries++
		mu.Unlock()
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()

	dispatcher := notify.NewWebhookDispatcher(notify.WebhookConfig{
		URL:         server.URL,
		MaxAttempts: 3,
		BaseBackoff: time.Millisecond,
	})

	err := dispatcher.DispatchDossierUpdate(context.Background(), notify.DossierWebhookEvent{ConversationID: "conv-fail"})
	if err == nil {
		t.Fatal("expected error when all attempts fail")
	}

	mu.Lock()
	defer mu.Unlock()
	if deliveries != 3 {
		t.Fatalf("expected exactly 3 attempts, got %d", deliveries)
	}
}

// TestWebhookDispatch_NoopWhenDisabled proves an empty URL short-circuits delivery.
func TestWebhookDispatch_NoopWhenDisabled(t *testing.T) {
	dispatcher := notify.NewWebhookDispatcher(notify.WebhookConfig{})
	if dispatcher.Enabled() {
		t.Fatal("expected dispatcher to be disabled without a URL")
	}
	if err := dispatcher.DispatchDossierUpdate(context.Background(), notify.DossierWebhookEvent{}); err != nil {
		t.Fatalf("expected nil error when disabled, got %v", err)
	}
}

// TestWebhookDispatch_AuthHeadersAndSignature proves bearer auth and HMAC signing are applied.
func TestWebhookDispatch_AuthHeadersAndSignature(t *testing.T) {
	secret := "top-secret-hmac"
	var mu sync.Mutex
	var capturedAuth string
	var capturedSignature string
	var capturedBody []byte

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		capturedAuth = r.Header.Get("Authorization")
		capturedSignature = r.Header.Get("X-Webhook-Signature")
		capturedBody = body
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	dispatcher := notify.NewWebhookDispatcher(notify.WebhookConfig{
		URL:         server.URL,
		BearerToken: "token-abc-123",
		HMACSecret:  secret,
	})

	if err := dispatcher.DispatchDossierUpdate(context.Background(), notify.DossierWebhookEvent{ConversationID: "conv-auth"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	if capturedAuth != "Bearer token-abc-123" {
		t.Errorf("expected bearer token header, got %q", capturedAuth)
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(capturedBody)
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if capturedSignature != want {
		t.Errorf("expected signature %q, got %q", want, capturedSignature)
	}
}

// TestWebhookDispatch_ContextCanceledStopsRetries proves cancellation aborts backoff promptly.
func TestWebhookDispatch_ContextCanceledStopsRetries(t *testing.T) {
	firstAttempt := make(chan struct{})
	var once sync.Once
	var mu sync.Mutex
	deliveries := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		deliveries++
		mu.Unlock()
		once.Do(func() { close(firstAttempt) })
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	dispatcher := notify.NewWebhookDispatcher(notify.WebhookConfig{
		URL:         server.URL,
		MaxAttempts: 5,
		BaseBackoff: time.Hour,
	})

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- dispatcher.DispatchDossierUpdate(ctx, notify.DossierWebhookEvent{ConversationID: "conv-cancel"})
	}()

	<-firstAttempt
	cancel()

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("dispatcher did not honor cancellation")
	}

	mu.Lock()
	defer mu.Unlock()
	if deliveries != 1 {
		t.Fatalf("expected no retries after cancellation, got %d deliveries", deliveries)
	}
}
