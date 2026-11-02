//go:build live

package gm

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPairingHandler_Start_Live exercises the real Google Messages registration path
// (QR challenge generation). It is quarantined behind the `live` build tag and requires
// explicit opt-in, so hermetic CI never touches the network (HARN-03, HARN-04).
//
// Run manually:
//
//	GM_ALLOW_NETWORK=1 LIVE_GM_TEST=1 SESSION_PATH=/tmp/live-session.json \
//	  go test -tags live -race -run TestPairingHandler_Start_Live ./internal/gm/
func TestPairingHandler_Start_Live(t *testing.T) {
	if os.Getenv("LIVE_GM_TEST") != "1" {
		t.Skip("live test quarantined (HARN-03): set LIVE_GM_TEST=1 to run against real Google registration")
	}

	sessionPath := os.Getenv("SESSION_PATH")
	if sessionPath == "" {
		sessionPath = filepath.Join(t.TempDir(), "session.json")
	}

	client, err := NewClient(ClientOptions{
		SessionPath: sessionPath,
	})
	require.NoError(t, err)

	handler := NewPairingHandler(client)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err = handler.Start(ctx)
	require.NoError(t, err, "live registration should return a QR challenge")
	assert.NotEmpty(t, handler.currentQR)
	assert.NotEmpty(t, handler.qrImage)
}
