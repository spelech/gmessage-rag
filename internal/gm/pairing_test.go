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

// TestPairingHandlers verifies AUTH-02 Web Mode HTTP handler serving QR PNG, JSON status, and HTML auto-refresh.
func TestPairingHandlers(t *testing.T) {
	tempDir := t.TempDir()
	sessionPath := filepath.Join(tempDir, "session.json")

	client, err := NewClient(ClientOptions{
		SessionPath: sessionPath,
	})
	require.NoError(t, err)

	handler := NewPairingHandler(client)

	// Case 1: GET /pair before QR code is ready
	req := httptest.NewRequest(http.MethodGet, "/pair", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Contains(t, rec.Body.String(), "Generating Pairing QR Code")

	// Case 2: Set QR code and request HTML
	mockQR := "https://messages.google.com/web/authentication?c=mock-qr-test-code"
	err = handler.SetQR(mockQR)
	require.NoError(t, err)

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "Scan to Pair with Google Messages")
	assert.Contains(t, rec.Body.String(), `<img src="/pair?format=png"`)

	// Case 3: GET /pair?format=png returns valid image
	pngReq := httptest.NewRequest(http.MethodGet, "/pair?format=png", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, pngReq)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "image/png", rec.Header().Get("Content-Type"))
	assert.NotEmpty(t, rec.Body.Bytes())

	// Case 4: GET /pair?format=json returns status
	jsonReq := httptest.NewRequest(http.MethodGet, "/pair?format=json", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, jsonReq)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"has_qr":true`)
	assert.Contains(t, rec.Body.String(), `"paired":false`)

	// Case 5: Mark as paired
	handler.MarkPaired()

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "Google Messages is Paired")

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, jsonReq)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"paired":true`)

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, pngReq)
	assert.Equal(t, http.StatusGone, rec.Code)
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
