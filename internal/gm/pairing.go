package gm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/mdp/qrterminal/v3"
	"github.com/skip2/go-qrcode"
	"go.mau.fi/mautrix-gmessages/pkg/libgm"
)

// SessionFileMode is the POSIX file mode required for session token security (SEC-01 / AUTH-01).
const SessionFileMode = 0600

// LoadAuthData loads *libgm.AuthData from path if it exists; otherwise creates new AuthData.
func LoadAuthData(path string) (*libgm.AuthData, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return libgm.NewAuthData(), nil
		}
		return nil, fmt.Errorf("failed to read session file %s: %w", path, err)
	}

	authData := &libgm.AuthData{}
	if err := json.Unmarshal(data, authData); err != nil {
		return nil, fmt.Errorf("failed to parse session json: %w", err)
	}

	return authData, nil
}

// SaveAuthData atomically writes *libgm.AuthData to path.tmp with POSIX mode 0600 and renames to path.
func SaveAuthData(path string, authData *libgm.AuthData) error {
	if authData == nil {
		return errors.New("cannot save nil auth data")
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("failed to create auth directory %s: %w", dir, err)
	}

	jsonData, err := json.MarshalIndent(authData, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal auth data: %w", err)
	}

	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, jsonData, SessionFileMode); err != nil {
		return fmt.Errorf("failed to write tmp session file %s: %w", tmpPath, err)
	}

	// Double check file permissions before rename
	if err := os.Chmod(tmpPath, SessionFileMode); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to enforce 0600 permissions on %s: %w", tmpPath, err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to rename %s to %s: %w", tmpPath, path, err)
	}

	return nil
}

// PairCLI starts login, prints the QR code in terminal stdout, and blocks until pairing succeeds.
func (c *Client) PairCLI(ctx context.Context) error {
	qrData, err := c.libgmClient.StartLogin(ctx)
	if err != nil {
		return fmt.Errorf("failed to start login: %w", err)
	}

	c.mu.Lock()
	c.currentQR = qrData
	c.mu.Unlock()

	fmt.Println("\nScan the QR code below in the Google Messages app (Device Pairing):")
	qrConfig := qrterminal.Config{
		Level:     qrterminal.L,
		Writer:    os.Stdout,
		BlackChar: qrterminal.BLACK,
		WhiteChar: qrterminal.WHITE,
		QuietZone: 1,
	}
	qrterminal.GenerateWithConfig(qrData, qrConfig)
	fmt.Println("\nWaiting for device pairing approval...")

	select {
	case <-c.pairDone:
		fmt.Println("Pairing successful! Session persisted.")
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// PairingHandler provides HTTP handlers for Web Mode pairing (GET /pair).
type PairingHandler struct {
	client      *Client
	mu          sync.RWMutex
	currentQR   string
	isPaired    bool
	qrImage     []byte
	lastUpdated time.Time
	listenOnce  sync.Once
}

// NewPairingHandler creates a PairingHandler for web QR pairing.
func NewPairingHandler(client *Client) *PairingHandler {
	return &PairingHandler{
		client: client,
	}
}

// Start initiates the login session in background if not already paired or in-flight.
func (h *PairingHandler) Start(ctx context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.client.libgmClient.IsLoggedIn() {
		h.isPaired = true
		return nil
	}

	qrData, err := h.client.libgmClient.StartLogin(ctx)
	if err != nil {
		return err
	}

	png, err := qrcode.Encode(qrData, qrcode.Medium, 320)
	if err != nil {
		return fmt.Errorf("failed to generate qr png: %w", err)
	}

	h.currentQR = qrData
	h.qrImage = png
	h.lastUpdated = time.Now()

	h.listenOnce.Do(func() {
		go func() {
			select {
			case <-h.client.pairDone:
				h.mu.Lock()
				h.isPaired = true
				h.mu.Unlock()
			case <-ctx.Done():
			}
		}()
	})

	return nil
}

// SetQR explicitly sets QR data (useful for testing or manual refreshes).
func (h *PairingHandler) SetQR(qrData string) error {
	png, err := qrcode.Encode(qrData, qrcode.Medium, 320)
	if err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.currentQR = qrData
	h.qrImage = png
	h.lastUpdated = time.Now()
	return nil
}

// MarkPaired marks the handler as paired.
func (h *PairingHandler) MarkPaired() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.isPaired = true
}

// ServeHTTP handles GET /pair requests.
// Defaults to Google Account (Gaia) emoji challenge pairing (AUTH-05).
// Legacy QR code pairing is accessible via ?mode=qr or ?format=png.
func (h *PairingHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	h.mu.RLock()
	isPaired := h.isPaired || (h.client != nil && h.client.libgmClient != nil && h.client.libgmClient.IsLoggedIn())
	qrImage := h.qrImage
	currentQR := h.currentQR
	h.mu.RUnlock()

	mode := r.URL.Query().Get("mode")
	format := r.URL.Query().Get("format")

	currentEmoji := ""
	if h.client != nil {
		currentEmoji = h.client.CurrentEmoji()
	}

	// If mode=qr or format=png, handle QR pairing flow (legacy fallback)
	if mode == "qr" || format == "png" {
		h.serveQR(w, isPaired, format, qrImage, currentQR)
		return
	}

	// Default: Gaia Emoji Challenge mode (AUTH-05)
	if format == "json" {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"mode":      "gaia",
			"paired":    isPaired,
			"emoji":     currentEmoji,
			"has_emoji": len(currentEmoji) > 0,
		})
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if isPaired {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `<!DOCTYPE html>
<html>
<head><title>Google Messages Pairing</title></head>
<body style="font-family: sans-serif; text-align: center; padding-top: 50px;">
  <h2>&#x2705; Google Messages is Paired!</h2>
  <p>The session token is active and persisted to disk.</p>
</body>
</html>`)
		return
	}

	if len(currentEmoji) == 0 {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `<!DOCTYPE html>
<html>
<head>
  <title>Google Messages Pairing - Gaia Challenge</title>
  <meta http-equiv="refresh" content="3">
</head>
<body style="font-family: sans-serif; text-align: center; padding-top: 50px;">
  <h2>Google Account (Gaia) Emoji Pairing</h2>
  <p>Initiating pairing challenge... please wait.</p>
  <div style="font-size: 48px; margin: 30px 0;">&#x23F3;</div>
  <p style="color: #666;"><small>Auto-refreshing every 3 seconds until challenge arrives.</small></p>
  <hr style="max-width: 400px; margin: 30px auto;" />
  <p><a href="/pair?mode=qr" style="color: #2563eb; text-decoration: underline;">Switch to legacy QR pairing</a></p>
</body>
</html>`)
		return
	}

	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head>
  <title>Google Messages Pairing - Gaia Challenge</title>
  <meta http-equiv="refresh" content="5">
</head>
<body style="font-family: sans-serif; text-align: center; padding-top: 50px;">
  <h2>Confirm Pairing on your Phone</h2>
  <p>Open Google Messages on your phone &gt; Device pairing, and tap this emoji:</p>
  <div style="font-size: 72px; margin: 24px 0; padding: 16px; background: #f1f5f9; display: inline-block; border-radius: 12px; border: 2px dashed #cbd5e1;">%s</div>
  <p><small>This page auto-refreshes every 5 seconds until pairing completes.</small></p>
  <hr style="max-width: 400px; margin: 30px auto;" />
  <p><a href="/pair?mode=qr" style="color: #2563eb; text-decoration: underline;">Switch to legacy QR pairing</a></p>
</body>
</html>`, currentEmoji)
}

func (h *PairingHandler) serveQR(w http.ResponseWriter, isPaired bool, format string, qrImage []byte, currentQR string) {
	if format == "json" {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"mode":    "qr",
			"paired":  isPaired,
			"has_qr":  len(currentQR) > 0,
			"qr_data": currentQR,
		})
		return
	}

	if format == "png" {
		if isPaired {
			http.Error(w, "Already paired", http.StatusGone)
			return
		}
		if len(qrImage) == 0 {
			http.Error(w, "QR code not ready yet", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(qrImage)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if isPaired {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `<!DOCTYPE html>
<html>
<head><title>Google Messages Pairing</title></head>
<body style="font-family: sans-serif; text-align: center; padding-top: 50px;">
  <h2>&#x2705; Google Messages is Paired!</h2>
  <p>The session token is active and persisted to disk.</p>
</body>
</html>`)
		return
	}

	if len(qrImage) == 0 {
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprint(w, `<!DOCTYPE html>
<html>
<head>
  <title>Google Messages Pairing (Legacy QR)</title>
  <meta http-equiv="refresh" content="2">
</head>
<body style="font-family: sans-serif; text-align: center; padding-top: 50px;">
  <h2>Generating Pairing QR Code...</h2>
  <p>Please wait while the relay registers with Google Messages.</p>
  <hr style="max-width: 400px; margin: 30px auto;" />
  <p><a href="/pair" style="color: #2563eb; text-decoration: underline;">Switch to Google Account emoji challenge (recommended)</a></p>
</body>
</html>`)
		return
	}

	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, `<!DOCTYPE html>
<html>
<head>
  <title>Google Messages Pairing (Legacy QR)</title>
  <meta http-equiv="refresh" content="5">
</head>
<body style="font-family: sans-serif; text-align: center; padding-top: 50px;">
  <h2>Scan to Pair with Google Messages</h2>
  <p>Open Google Messages on your phone &gt; Device pairing &gt; QR code scanner</p>
  <div style="margin: 20px auto;">
    <img src="/pair?mode=qr&format=png" alt="Google Messages QR Code" style="border: 1px solid #ccc; padding: 10px; border-radius: 8px;" />
  </div>
  <p><small>This page auto-refreshes every 5 seconds until pairing completes.</small></p>
  <hr style="max-width: 400px; margin: 30px auto;" />
  <p><a href="/pair" style="color: #2563eb; text-decoration: underline;">Switch to Google Account emoji challenge (recommended)</a></p>
</body>
</html>`)
}
