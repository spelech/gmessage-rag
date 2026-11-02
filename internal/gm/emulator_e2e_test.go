package gm

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spelech/gmessage-rag/internal/gm/testing/emulator"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mau.fi/mautrix-gmessages/pkg/libgm"
)

// TestEmulator_StartLoginReturnsQR proves the HARN-02 emulator can drive the real
// libgm pairing handshake hermetically: RegisterPhoneRelay is served in-process and
// StartLogin returns a QR challenge without any network access.
func TestEmulator_StartLoginReturnsQR(t *testing.T) {
	emu := emulator.NewTransport()

	client, err := NewClient(ClientOptions{
		SessionPath:        filepath.Join(t.TempDir(), "session.json"),
		HTTPClientSettings: emu.Settings(),
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	qr, err := client.LibGM().StartLogin(ctx)
	require.NoError(t, err, "emulated RegisterPhoneRelay should succeed")
	assert.NotEmpty(t, qr)
	assert.True(t, strings.HasPrefix(qr, "https://support.google.com/messages/"),
		"unexpected QR payload prefix: %s", qr)

	assert.Contains(t, emu.Requests(),
		"/$rpc/google.internal.communications.instantmessaging.v1.Pairing/RegisterPhoneRelay")
}

// TestEmulator_PairingCompletesPairDone proves the emulator's long-poll injection
// drives the real gm.Client event loop to a completed pairing: client.pairDone
// closes and the session file is persisted with the paired browser device.
func TestEmulator_PairingCompletesPairDone(t *testing.T) {
	emu := emulator.NewTransport()
	sessionPath := filepath.Join(t.TempDir(), "session.json")

	client, err := NewClient(ClientOptions{
		SessionPath:        sessionPath,
		HTTPClientSettings: emu.Settings(),
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	qr, err := client.LibGM().StartLogin(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, qr)

	select {
	case <-client.pairDone:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for emulated pairing event")
	}

	raw, err := os.ReadFile(sessionPath)
	require.NoError(t, err, "PairSuccessful should persist the session")

	var auth libgm.AuthData
	require.NoError(t, json.Unmarshal(raw, &auth))
	require.NotNil(t, auth.Browser, "paired browser device should be persisted")
	assert.Equal(t, "emulator-browser", auth.Browser.SourceID)
	assert.NotEmpty(t, auth.TachyonAuthToken)

	assert.Contains(t, emu.Requests(),
		"/$rpc/google.internal.communications.instantmessaging.v1.Pairing/RegisterPhoneRelay")
	assert.Contains(t, emu.Requests(),
		"/$rpc/google.internal.communications.instantmessaging.v1.Messaging/ReceiveMessages")
}
