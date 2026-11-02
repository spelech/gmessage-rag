package notify_test

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/spelech/gmessage-rag/internal/notify"
)

// Requirement ID: TRANS-04
func TestWatchdog_PhoneDisconnect_GracePeriodTrigger(t *testing.T) {
	var mu sync.Mutex
	var receivedAlerts []capturedRequest

	ntfyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		receivedAlerts = append(receivedAlerts, capturedRequest{
			Headers: r.Header.Clone(),
			Body:    string(body),
		})
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer ntfyServer.Close()

	client := notify.NewClient(notify.ClientConfig{
		NtfyURL: ntfyServer.URL,
	})

	// Use short grace period for fast deterministic testing
	grace := 50 * time.Millisecond
	wd := notify.NewWatchdog(notify.WatchdogConfig{
		NotifyClient:            client,
		NotifyOnPhoneDisconnect: true,
		GracePeriod:             grace,
		Logger:                  zerolog.Nop(),
	})

	// Phone disconnects
	wd.OnPhoneNotResponding()
	if !wd.IsTimerActive() {
		t.Fatalf("expected timer to be active")
	}

	// Calling OnPhoneNotResponding again while timer running is a no-op
	wd.OnPhoneNotResponding()

	// Wait for grace period to elapse
	time.Sleep(120 * time.Millisecond)

	if wd.IsTimerActive() {
		t.Errorf("expected timer to no longer be active after firing")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(receivedAlerts) != 1 {
		t.Fatalf("expected 1 disconnect alert, got %d", len(receivedAlerts))
	}
	expectedTitle := "Google Messages Phone Disconnected"
	if receivedAlerts[0].Headers.Get("Title") != expectedTitle {
		t.Errorf("expected title '%s', got '%s'", expectedTitle, receivedAlerts[0].Headers.Get("Title"))
	}
	if receivedAlerts[0].Headers.Get("Priority") != "high" {
		t.Errorf("expected high priority, got %s", receivedAlerts[0].Headers.Get("Priority"))
	}
}

// Requirement ID: TRANS-04
func TestWatchdog_PhoneDisconnect_ReconnectedWithinGracePeriod(t *testing.T) {
	var mu sync.Mutex
	alertFired := false

	ntfyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		alertFired = true
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer ntfyServer.Close()

	client := notify.NewClient(notify.ClientConfig{
		NtfyURL: ntfyServer.URL,
	})

	grace := 100 * time.Millisecond
	wd := notify.NewWatchdog(notify.WatchdogConfig{
		NotifyClient:            client,
		NotifyOnPhoneDisconnect: true,
		GracePeriod:             grace,
		Logger:                  zerolog.Nop(),
	})

	// Drop occurs
	wd.OnPhoneNotResponding()
	if !wd.IsTimerActive() {
		t.Fatalf("expected timer active")
	}

	// Brief interruption: phone reconnects after 20ms (< 100ms grace period)
	time.Sleep(20 * time.Millisecond)
	wd.OnPhoneRespondingAgain()

	if wd.IsTimerActive() {
		t.Fatalf("expected timer to be cancelled immediately")
	}

	// Wait past the original grace period duration
	time.Sleep(120 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if alertFired {
		t.Errorf("expected zero alerts due to quick reconnection within grace period")
	}
}

// Requirement ID: TRANS-04
func TestWatchdog_DisabledPhoneDisconnectAlerts(t *testing.T) {
	wd := notify.NewWatchdog(notify.WatchdogConfig{
		NotifyOnPhoneDisconnect: false,
		GracePeriod:             50 * time.Millisecond,
		Logger:                  zerolog.Nop(),
	})

	wd.OnPhoneNotResponding()
	if wd.IsTimerActive() {
		t.Errorf("expected timer not to start when NotifyOnPhoneDisconnect is false")
	}
}

// Requirement ID: AUTH-03
func TestWatchdog_OnListenFatalError_TriggersImmediateAlert(t *testing.T) {
	var mu sync.Mutex
	var alerts []capturedRequest

	ntfyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		alerts = append(alerts, capturedRequest{
			Headers: r.Header.Clone(),
			Body:    string(body),
		})
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer ntfyServer.Close()

	client := notify.NewClient(notify.ClientConfig{
		NtfyURL:        ntfyServer.URL,
		ExternalWebURL: "http://localhost:8090",
	})

	wd := notify.NewWatchdog(notify.WatchdogConfig{
		NotifyClient: client,
		Logger:       zerolog.Nop(),
	})

	fatalErr := errors.New("socket broken / authentication revoked")
	wd.OnListenFatalError(fatalErr)

	mu.Lock()
	defer mu.Unlock()
	if len(alerts) != 1 {
		t.Fatalf("expected 1 immediate alert on fatal error, got %d", len(alerts))
	}
	if alerts[0].Headers.Get("Title") != "Google Messages Pairing Required" {
		t.Errorf("unexpected alert title: %s", alerts[0].Headers.Get("Title"))
	}
}
