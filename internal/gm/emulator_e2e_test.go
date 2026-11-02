package gm

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spelech/gmessage-rag/internal/gm/testing/emulator"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
