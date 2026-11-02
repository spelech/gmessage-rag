package notify_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/spelech/gmessage-rag/internal/notify"
)

type capturedRequest struct {
	Method  string
	Path    string
	Headers http.Header
	Body    string
}

// Requirement ID: AUTH-03
func TestPushAlerting_ReAuthNotification(t *testing.T) {
	var mu sync.Mutex
	var ntfyReqs []capturedRequest
	var appriseReqs []capturedRequest

	ntfyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		ntfyReqs = append(ntfyReqs, capturedRequest{
			Method:  r.Method,
			Path:    r.URL.Path,
			Headers: r.Header.Clone(),
			Body:    string(body),
		})
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer ntfyServer.Close()

	appriseServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		appriseReqs = append(appriseReqs, capturedRequest{
			Method:  r.Method,
			Path:    r.URL.Path,
			Headers: r.Header.Clone(),
			Body:    string(body),
		})
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer appriseServer.Close()

	client := notify.NewClient(notify.ClientConfig{
		AppriseURL:     appriseServer.URL + "/notify",
		NtfyURL:        ntfyServer.URL + "/my-topic",
		ExternalWebURL: "https://messages.example.com",
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := client.SendSessionExpiredAlert(ctx)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	// Assert ntfy request
	if len(ntfyReqs) != 1 {
		t.Fatalf("expected 1 ntfy request, got %d", len(ntfyReqs))
	}
	ntfyReq := ntfyReqs[0]
	if ntfyReq.Headers.Get("Title") != "Google Messages Pairing Required" {
		t.Errorf("unexpected ntfy title: %s", ntfyReq.Headers.Get("Title"))
	}
	if ntfyReq.Headers.Get("Priority") != "urgent" {
		t.Errorf("expected urgent priority, got %s", ntfyReq.Headers.Get("Priority"))
	}
	if ntfyReq.Headers.Get("Tags") != "warning,mobile_phone" {
		t.Errorf("unexpected tags: %s", ntfyReq.Headers.Get("Tags"))
	}
	expectedAction := "view, Re-pair Session, https://messages.example.com/pair, clear=true"
	if ntfyReq.Headers.Get("Actions") != expectedAction {
		t.Errorf("expected Actions '%s', got '%s'", expectedAction, ntfyReq.Headers.Get("Actions"))
	}
	expectedBody := "Google Messages web session has expired or unpaired. Tap to re-pair on mobile."
	if ntfyReq.Body != expectedBody {
		t.Errorf("expected body '%s', got '%s'", expectedBody, ntfyReq.Body)
	}

	// Assert Apprise request
	if len(appriseReqs) != 1 {
		t.Fatalf("expected 1 apprise request, got %d", len(appriseReqs))
	}
	appReq := appriseReqs[0]
	if appReq.Headers.Get("Content-Type") != "application/json" {
		t.Errorf("expected application/json, got %s", appReq.Headers.Get("Content-Type"))
	}
	var appPayload notify.ApprisePayload
	if err := json.Unmarshal([]byte(appReq.Body), &appPayload); err != nil {
		t.Fatalf("failed to decode json body: %v", err)
	}
	if appPayload.Title != "Google Messages Pairing Required" {
		t.Errorf("unexpected apprise title: %s", appPayload.Title)
	}
	if appPayload.Body != expectedBody {
		t.Errorf("unexpected apprise body: %s", appPayload.Body)
	}
	if appPayload.URL != "https://messages.example.com/pair" {
		t.Errorf("unexpected apprise url: %s", appPayload.URL)
	}
}

// Requirement ID: AUTH-04
func TestPushAlerting_GaiaChallengeAlert(t *testing.T) {
	var ntfyReq capturedRequest
	ntfyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		ntfyReq = capturedRequest{
			Method:  r.Method,
			Headers: r.Header.Clone(),
			Body:    string(body),
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ntfyServer.Close()

	client := notify.NewClient(notify.ClientConfig{
		NtfyURL:        ntfyServer.URL,
		ExternalWebURL: "http://localhost:8090",
	})

	ctx := context.Background()
	emoji := "🚀"
	if err := client.SendGaiaChallengeAlert(ctx, emoji); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expectedTitle := "Google Messages Pairing Challenge: 🚀"
	if ntfyReq.Headers.Get("Title") != expectedTitle {
		t.Errorf("expected title '%s', got '%s'", expectedTitle, ntfyReq.Headers.Get("Title"))
	}
	expectedBody := "Tap Google Messages on your phone and select: 🚀"
	if ntfyReq.Body != expectedBody {
		t.Errorf("expected body '%s', got '%s'", expectedBody, ntfyReq.Body)
	}
	if ntfyReq.Headers.Get("Click") != "http://localhost:8090/pair/gaia" {
		t.Errorf("expected click url http://localhost:8090/pair/gaia, got %s", ntfyReq.Headers.Get("Click"))
	}
}

func TestPushAlerting_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := notify.NewClient(notify.ClientConfig{
		NtfyURL: server.URL,
	})

	err := client.SendSessionExpiredAlert(context.Background())
	if err == nil {
		t.Fatalf("expected error on 500 response, got nil")
	}
}

func TestPushAlerting_EmptyEndpointsNoop(t *testing.T) {
	client := notify.NewClient(notify.ClientConfig{})
	err := client.SendSessionExpiredAlert(context.Background())
	if err != nil {
		t.Fatalf("expected nil error when no endpoints configured, got %v", err)
	}
}
