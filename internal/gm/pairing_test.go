package gm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mau.fi/mautrix-gmessages/pkg/libgm"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/events"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
)

// TestSessionPersistence_Permissions verifies AUTH-01 and SEC-01 requirements:
// POSIX 0600 file permissions and atomic writes on PairSuccessful and AuthTokenRefreshed.
func TestSessionPersistence_Permissions(t *testing.T) {
	tempDir := t.TempDir()
	sessionPath := filepath.Join(tempDir, "auth", "session.json")

	// 1. Loading nonexistent path creates fresh libgm.NewAuthData()
	authData, err := LoadAuthData(sessionPath)
	require.NoError(t, err)
	require.NotNil(t, authData)
	require.NotNil(t, authData.RequestCrypto)
	require.NotNil(t, authData.RefreshKey)

	// Modify authData with mock session state
	testSessionID := uuid.New()
	authData.SessionID = testSessionID
	authData.TachyonAuthToken = []byte("super-secret-tachyon-token")
	authData.TachyonTTL = 3600

	// 2. SaveAuthData atomically writes with mode 0600
	err = SaveAuthData(sessionPath, authData)
	require.NoError(t, err)

	info, err := os.Stat(sessionPath)
	require.NoError(t, err)
	perm := info.Mode().Perm()
	assert.Equal(t, os.FileMode(0600), perm, "session.json must have POSIX 0600 permissions")

	// Verify temp file does not remain
	_, err = os.Stat(sessionPath + ".tmp")
	assert.True(t, os.IsNotExist(err), "temp session file should be renamed away")

	// 3. Reload persisted data and verify fields
	reloaded, err := LoadAuthData(sessionPath)
	require.NoError(t, err)
	assert.Equal(t, authData.SessionID, reloaded.SessionID)
	assert.Equal(t, []byte("super-secret-tachyon-token"), reloaded.TachyonAuthToken)
	assert.Equal(t, int64(3600), reloaded.TachyonTTL)

	// 4. Verify client event triggers persist with 0600
	client, err := NewClient(ClientOptions{
		SessionPath: sessionPath,
	})
	require.NoError(t, err)

	// Update token and trigger AuthTokenRefreshed event
	client.AuthData().TachyonAuthToken = []byte("refreshed-token-12345")
	client.handleEvent(&events.AuthTokenRefreshed{})

	reloadedAfterRefresh, err := LoadAuthData(sessionPath)
	require.NoError(t, err)
	assert.Equal(t, []byte("refreshed-token-12345"), reloadedAfterRefresh.TachyonAuthToken)

	infoAfterRefresh, err := os.Stat(sessionPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), infoAfterRefresh.Mode().Perm())

	// Update token and trigger PairSuccessful event
	client.AuthData().TachyonAuthToken = []byte("pair-success-token")
	client.handleEvent(&events.PairSuccessful{
		PhoneID: "phone-1",
	})

	reloadedAfterPair, err := LoadAuthData(sessionPath)
	require.NoError(t, err)
	assert.Equal(t, []byte("pair-success-token"), reloadedAfterPair.TachyonAuthToken)

	infoAfterPair, err := os.Stat(sessionPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), infoAfterPair.Mode().Perm())
}

// TestPairingHandlers verifies AUTH-02 and AUTH-05 Web Mode HTTP handler serving:
// - Default Google Account Gaia emoji challenge flow (AUTH-05)
// - Fallback legacy QR flow via ?mode=qr and ?format=png (AUTH-02)
func TestPairingHandlers(t *testing.T) {
	tempDir := t.TempDir()
	sessionPath := filepath.Join(tempDir, "session.json")

	client, err := NewClient(ClientOptions{
		SessionPath: sessionPath,
	})
	require.NoError(t, err)

	handler := NewPairingHandler(client)

	// Case 1: Default GET /pair renders Gaia emoji challenge flow (AUTH-05)
	// When emoji is not yet ready:
	req := httptest.NewRequest(http.MethodGet, "/pair", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "Google Account (Gaia) Emoji Pairing")
	assert.Contains(t, rec.Body.String(), "Switch to legacy QR pairing")

	// When emoji is set on client:
	client.mu.Lock()
	client.currentEmoji = "🚀"
	client.mu.Unlock()

	recEmoji := httptest.NewRecorder()
	handler.ServeHTTP(recEmoji, req)
	assert.Equal(t, http.StatusOK, recEmoji.Code)
	assert.Contains(t, recEmoji.Body.String(), "Confirm Pairing on your Phone")
	assert.Contains(t, recEmoji.Body.String(), "🚀")
	assert.Contains(t, recEmoji.Body.String(), `<a href="/pair?mode=qr"`)

	// Case 2: Default GET /pair?format=json returns Gaia challenge JSON
	jsonReq := httptest.NewRequest(http.MethodGet, "/pair?format=json", nil)
	recJSON := httptest.NewRecorder()
	handler.ServeHTTP(recJSON, jsonReq)
	assert.Equal(t, http.StatusOK, recJSON.Code)
	assert.Contains(t, recJSON.Body.String(), `"mode":"gaia"`)
	assert.Contains(t, recJSON.Body.String(), `"emoji":"🚀"`)
	assert.Contains(t, recJSON.Body.String(), `"has_emoji":true`)
	assert.Contains(t, recJSON.Body.String(), `"paired":false`)

	// Case 3: GET /pair?mode=qr before QR code is set (returns 503)
	qrReq := httptest.NewRequest(http.MethodGet, "/pair?mode=qr", nil)
	recQR := httptest.NewRecorder()
	handler.ServeHTTP(recQR, qrReq)
	assert.Equal(t, http.StatusServiceUnavailable, recQR.Code)
	assert.Contains(t, recQR.Body.String(), "Generating Pairing QR Code")
	assert.Contains(t, recQR.Body.String(), "Switch to Google Account emoji challenge")

	// Case 4: Set QR code and request GET /pair?mode=qr
	mockQR := "https://messages.google.com/web/authentication?c=mock-qr-test-code"
	err = handler.SetQR(mockQR)
	require.NoError(t, err)

	recQRReady := httptest.NewRecorder()
	handler.ServeHTTP(recQRReady, qrReq)
	assert.Equal(t, http.StatusOK, recQRReady.Code)
	assert.Contains(t, recQRReady.Body.String(), "Scan to Pair with Google Messages")
	assert.Contains(t, recQRReady.Body.String(), `<img src="/pair?mode=qr&format=png"`)
	assert.Contains(t, recQRReady.Body.String(), "Switch to Google Account emoji challenge")

	// Case 5: GET /pair?format=png returns valid image
	pngReq := httptest.NewRequest(http.MethodGet, "/pair?format=png", nil)
	recPNG := httptest.NewRecorder()
	handler.ServeHTTP(recPNG, pngReq)
	assert.Equal(t, http.StatusOK, recPNG.Code)
	assert.Equal(t, "image/png", recPNG.Header().Get("Content-Type"))
	assert.NotEmpty(t, recPNG.Body.Bytes())

	// Case 6: GET /pair?mode=qr&format=json returns QR status
	jsonQRReq := httptest.NewRequest(http.MethodGet, "/pair?mode=qr&format=json", nil)
	recQRJSON := httptest.NewRecorder()
	handler.ServeHTTP(recQRJSON, jsonQRReq)
	assert.Equal(t, http.StatusOK, recQRJSON.Code)
	assert.Contains(t, recQRJSON.Body.String(), `"mode":"qr"`)
	assert.Contains(t, recQRJSON.Body.String(), `"has_qr":true`)
	assert.Contains(t, recQRJSON.Body.String(), `"paired":false`)

	// Case 7: Mark as paired
	handler.MarkPaired()

	recPaired := httptest.NewRecorder()
	handler.ServeHTTP(recPaired, req)
	assert.Equal(t, http.StatusOK, recPaired.Code)
	assert.Contains(t, recPaired.Body.String(), "Google Messages is Paired")

	recPairedQR := httptest.NewRecorder()
	handler.ServeHTTP(recPairedQR, qrReq)
	assert.Equal(t, http.StatusOK, recPairedQR.Code)
	assert.Contains(t, recPairedQR.Body.String(), "Google Messages is Paired")

	recPairedJSON := httptest.NewRecorder()
	handler.ServeHTTP(recPairedJSON, jsonReq)
	assert.Equal(t, http.StatusOK, recPairedJSON.Code)
	assert.Contains(t, recPairedJSON.Body.String(), `"paired":true`)

	recPairedPNG := httptest.NewRecorder()
	handler.ServeHTTP(recPairedPNG, pngReq)
	assert.Equal(t, http.StatusGone, recPairedPNG.Code)
}

func TestPairCLI_ContextCancel(t *testing.T) {
	tempDir := t.TempDir()
	sessionPath := filepath.Join(tempDir, "session.json")

	client, err := NewClient(ClientOptions{
		SessionPath: sessionPath,
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	// PairCLI should return context.Canceled if StartLogin fails or context cancels
	err = client.PairCLI(ctx)
	assert.Error(t, err)
}

func TestSaveAuthData_ErrorCases(t *testing.T) {
	tempDir := t.TempDir()

	// 1. Nil authData
	err := SaveAuthData(filepath.Join(tempDir, "session.json"), nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cannot save nil auth data")

	// 2. Directory creation failure
	authData := libgm.NewAuthData()
	err = SaveAuthData("/dev/null/forbidden/session.json", authData)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create auth directory")

	// 3. Write temp file failure (a directory already exists with the .tmp name)
	sessionPath := filepath.Join(tempDir, "session.json")
	require.NoError(t, os.Mkdir(sessionPath+".tmp", 0700))
	err = SaveAuthData(sessionPath, authData)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to write tmp session file")
}

func TestLoadAuthData_Corrupt(t *testing.T) {
	tempDir := t.TempDir()
	corruptFile := filepath.Join(tempDir, "bad.json")
	require.NoError(t, os.WriteFile(corruptFile, []byte("{invalid-json"), 0600))

	_, err := LoadAuthData(corruptFile)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse session json")
}

func TestPairingHandler_Start(t *testing.T) {
	tempDir := t.TempDir()
	sessionPath := filepath.Join(tempDir, "session.json")

	client, err := NewClient(ClientOptions{
		SessionPath: sessionPath,
	})
	require.NoError(t, err)

	handler := NewPairingHandler(client)

	// 1. Start login initializes QR code and image
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err = handler.Start(ctx)
	assert.NoError(t, err)
	assert.NotEmpty(t, handler.currentQR)
	assert.NotEmpty(t, handler.qrImage)

	// 2. When client is logged in, Start marks isPaired = true and returns nil immediately
	client2, err := NewClient(ClientOptions{
		SessionPath: filepath.Join(tempDir, "session2.json"),
	})
	require.NoError(t, err)
	client2.AuthData().Browser = &gmproto.Device{}
	client2.AuthData().SetCookies(map[string]string{"dummy": "cookie"})
	assert.True(t, client2.LibGM().IsLoggedIn())

	handler2 := NewPairingHandler(client2)
	err = handler2.Start(context.Background())
	assert.NoError(t, err)
	assert.True(t, handler2.isPaired)
}
