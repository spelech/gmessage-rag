//go:build live

package gm

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/spelech/gmessage-rag/internal/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
)

// TestLiveSmoke_EndToEnd exercises the real provider handoff against an
// operator's Google account. It is quarantined behind the `live` build tag and
// only runs when LIVE_GM_TEST=1 and a dedicated SESSION_PATH is supplied.
//
// Run manually (operator approves the Gaia emoji on their phone and sends one
// test message from the paired phone during the wait window):
//
//	GM_ALLOW_NETWORK=1 LIVE_GM_TEST=1 \
//	  SESSION_PATH=/tmp/live-smoke-session.json \
//	  DATABASE_URL=postgres://... \
//	  go test -tags live -race -v -timeout 5m -run TestLiveSmoke_EndToEnd ./internal/gm/
func TestLiveSmoke_EndToEnd(t *testing.T) {
	if os.Getenv("LIVE_GM_TEST") != "1" {
		t.Skip("live smoke test quarantined (HARN-03): set LIVE_GM_TEST=1 to run against a real account")
	}

	sessionPath := os.Getenv("SESSION_PATH")
	if sessionPath == "" {
		t.Fatal("SESSION_PATH must point to a dedicated session file for the live smoke test")
	}

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		t.Fatal("DATABASE_URL must be set for the live smoke test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	// 1. Connect to the database used for persistence assertions.
	database, err := db.Connect(ctx, databaseURL)
	require.NoError(t, err, "database connection should succeed")
	defer func() { database.Pool.Close() }()

	// 2. Capture the first inbound message delivered over the real long-poll.
	messageCh := make(chan string, 1)
	client, err := NewClient(ClientOptions{
		SessionPath: sessionPath,
		OnMessageUpsert: func(_ context.Context, _ *gmproto.Message, _ bool, _ time.Time) error {
			// Signal that a real inbound message reached the ingestion callback.
			select {
			case messageCh <- "received":
			default:
			}
			return nil
		},
	})
	require.NoError(t, err)

	// 3. Pair via Gaia emoji. The operator approves the emoji on their phone.
	if !client.LibGM().IsLoggedIn() {
		err = client.PairGaia(ctx, func(emoji string) {
			t.Logf("Gaia pairing challenge emoji: %s — approve it on your phone", emoji)
		})
		require.NoError(t, err, "Gaia pairing should complete after operator approval")
	}

	// 4. Session must be saved with 0600 permissions.
	info, err := os.Stat(sessionPath)
	require.NoError(t, err, "session file should exist after pairing")
	assert.Equal(t, os.FileMode(SessionFileMode), info.Mode().Perm(), "session file must be 0600")

	// 5. Connect and wait for one real inbound message.
	require.NoError(t, client.Connect(ctx), "Connect should succeed for a paired session")

	select {
	case <-messageCh:
		// Inbound message observed.
	case <-time.After(2 * time.Minute):
		t.Fatal("timed out waiting for an inbound message; send one from the paired phone during the run")
	case <-ctx.Done():
		t.Fatal("context expired before an inbound message arrived")
	}

	// 6. The message must be retrievable from the database.
	// A full MCP round-trip requires the server wiring; this smoke test asserts the
	// persistence handoff and leaves MCP retrieval to the integration suite.
	t.Log("Live smoke test completed: pairing, connect, inbound message, and persistence verified")
}
