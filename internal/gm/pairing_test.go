package gm

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mau.fi/mautrix-gmessages/pkg/libgm"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/events"
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
