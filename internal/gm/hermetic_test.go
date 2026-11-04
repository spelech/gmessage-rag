//go:build !live

package gm

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mau.fi/mautrix-gmessages/pkg/libgm"
	exhttp "go.mau.fi/util/exhttp"
)

// denyNetworkTransport rejects every outbound HTTP request during hermetic test runs (HARN-03).
type denyNetworkTransport struct{}

func (denyNetworkTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return nil, fmt.Errorf(
		"hermetic network guard (HARN-03): blocked outbound request to %q; run live tests with -tags live and LIVE_GM_TEST=1",
		req.URL.Host,
	)
}

// init activates the hermetic network guard for the entire `gm` test binary.
// It installs a deny-all transport into the libgm constructor seam unless the operator
// explicitly opts into live network access with GM_ALLOW_NETWORK=1. This keeps the default
// `go test ./...` run fully offline and fails loudly on any accidental egress.
func init() {
	if os.Getenv("GM_ALLOW_NETWORK") == "1" {
		return
	}

	base := libgm.NewClient
	newLibGMClient = func(
		ad *libgm.AuthData,
		pk *libgm.PushKeys,
		logger zerolog.Logger,
		settings exhttp.ClientSettings,
	) *libgm.Client {
		// Respect an explicitly provided transport (e.g. the in-process emulator in HARN-02).
		if settings.TransportOverride == nil {
			settings.TransportOverride = func(exhttp.ClientSettings) http.RoundTripper {
				return denyNetworkTransport{}
			}
		}
		return base(ad, pk, logger, settings)
	}
}

// TestHermeticGuard_BlocksOutboundRegistration proves the guard is active by driving the
// real libgm StartLogin path. StartLogin calls RegisterPhoneRelay with context.TODO(), which
// would otherwise POST to Google. Under the guard it must fail fast with our guard error.
func TestHermeticGuard_BlocksOutboundRegistration(t *testing.T) {
	client, err := NewClient(ClientOptions{
		SessionPath: filepath.Join(t.TempDir(), "session.json"),
	})
	require.NoError(t, err)

	_, err = client.LibGM().StartLogin(context.Background())
	require.Error(t, err, "guard must block the outbound registration attempt")
	assert.Contains(t, err.Error(), "hermetic network guard (HARN-03)")
}

// TestDenyNetworkTransport_RejectsRequests covers the deny-all guard transport directly.
func TestDenyNetworkTransport_RejectsRequests(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "https://messages.google.com/web/config", nil)
	require.NoError(t, err)

	_, err = denyNetworkTransport{}.RoundTrip(req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "blocked outbound request")
}
