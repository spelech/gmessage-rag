// Package emulator provides an in-process, hermetic implementation of the Google
// Messages HTTP RPC surface that libgm speaks (HARN-02).
//
// It is intended for tests only. Wire it into gm.ClientOptions through
// exhttp.ClientSettings.TransportOverride: no sockets, DNS, or TLS are involved.
//
// This first slice covers the pairing handshake surface needed to drive
// libgm.StartLogin to a QR challenge without touching Google. Remaining RPCs
// (RefreshPhoneRelay, SignInGaia, SendMessage, FetchMessages, ListContacts, media)
// are tracked in issue #62.
package emulator

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
	exhttp "go.mau.fi/util/exhttp"
	"google.golang.org/protobuf/proto"
)

// DefaultPairingKey is returned by the emulated RegisterPhoneRelay RPC.
var DefaultPairingKey = []byte("emulator-pairing-key")

// Transport routes libgm requests to in-process handlers by URL suffix.
// It implements http.RoundTripper.
type Transport struct {
	mu         sync.Mutex
	pairingKey []byte
	requests   []string
}

// NewTransport creates an emulator transport with default fixtures.
func NewTransport() *Transport {
	return &Transport{pairingKey: append([]byte(nil), DefaultPairingKey...)}
}

// Settings returns exhttp.ClientSettings that route every libgm request through t.
func (t *Transport) Settings() *exhttp.ClientSettings {
	return &exhttp.ClientSettings{
		TransportOverride: func(exhttp.ClientSettings) http.RoundTripper { return t },
	}
}

// Requests returns the URL paths handled so far, in order. Intended for assertions.
func (t *Transport) Requests() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.requests...)
}

// SetPairingKey overrides the pairing key returned by RegisterPhoneRelay.
func (t *Transport) SetPairingKey(key []byte) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pairingKey = append([]byte(nil), key...)
}

// RoundTrip dispatches a libgm request to the matching emulated RPC.
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	path := req.URL.Path

	t.mu.Lock()
	t.requests = append(t.requests, path)
	pairingKey := append([]byte(nil), t.pairingKey...)
	t.mu.Unlock()

	switch {
	case strings.HasSuffix(path, "/RegisterPhoneRelay"):
		return protobufResponse(req, &gmproto.RegisterPhoneRelayResponse{
			PairingKey: pairingKey,
			AuthKeyData: &gmproto.TokenData{
				TachyonAuthToken: []byte("emulator-tachyon-token"),
				TTL:              3600_000_000, // microseconds (1 hour)
			},
			Browser:    &gmproto.Device{SourceID: "emulator-browser"},
			ValidFor:   600,
			ResponseID: "emulator-register-response",
		})

	case strings.HasSuffix(path, "/web/config"):
		return protobufResponse(req, &gmproto.Config{
			ClientVersion: "2026.01.01",
			ServerVersion: "2026.01.01",
		})

	case strings.HasSuffix(path, "/ReceiveMessages"):
		// Long-poll: block until the caller cancels. Event injection lands with the
		// next slice of #62.
		<-req.Context().Done()
		return nil, req.Context().Err()

	default:
		return nil, fmt.Errorf("emulator (HARN-02): unhandled endpoint %q", path)
	}
}

func protobufResponse(req *http.Request, msg proto.Message) (*http.Response, error) {
	body, err := proto.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("emulator (HARN-02): marshal %T: %w", msg, err)
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type": []string{"application/x-protobuf"},
		},
		Body:    io.NopCloser(bytes.NewReader(body)),
		Request: req,
	}, nil
}
