// Package emulator provides an in-process, hermetic implementation of the Google
// Messages HTTP RPC surface that libgm speaks (HARN-02).
//
// It is intended for tests only. Wire it into gm.ClientOptions through
// exhttp.ClientSettings.TransportOverride: no sockets, DNS, or TLS are involved.
//
// This slice covers the pairing handshake surface needed to drive
// libgm.StartLogin to a QR challenge without touching Google, and injects the
// long-poll PairEvent so a full pairing completes (session persisted) hermetically.
// It also serves the post-pairing RPC surface needed for ingestion: FetchMessages
// (LIST_MESSAGES), ListContacts (LIST_CONTACTS), and media download (/upload).
// Remaining RPCs (RefreshPhoneRelay, SignInGaia, SendMessage, upload) are tracked
// in issue #62.
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
	"go.mau.fi/util/pblite"
	"google.golang.org/protobuf/proto"
)

// DefaultPairingKey is returned by the emulated RegisterPhoneRelay RPC.
var DefaultPairingKey = []byte("emulator-pairing-key")

// Transport routes libgm requests to in-process handlers by URL suffix.
// It implements http.RoundTripper.
type Transport struct {
	mu           sync.Mutex
	pairingKey   []byte
	requests     []string
	paired       bool
	responses    chan *gmproto.IncomingRPCMessage
	cipher       RequestCipher
	messagePages map[string]map[string]MessagePage
	contacts     []*gmproto.Contact
	media        map[string]mediaFixture
}

// NewTransport creates an emulator transport with default fixtures.
func NewTransport() *Transport {
	return &Transport{
		pairingKey:   append([]byte(nil), DefaultPairingKey...),
		responses:    make(chan *gmproto.IncomingRPCMessage, 32),
		messagePages: make(map[string]map[string]MessagePage),
		media:        make(map[string]mediaFixture),
	}
}

// SetPaired marks the transport as having already delivered the pairing event.
func (t *Transport) SetPaired(v bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.paired = v
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
				TTL:              86_400_000_000, // microseconds (24 hours)
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
		t.mu.Lock()
		deliverPair := !t.paired
		if deliverPair {
			t.paired = true
		}
		t.mu.Unlock()

		if deliverPair {
			return t.pairingResponse(req)
		}

		// Steady-state long-poll: deliver the next queued RPC response, or block
		// until one arrives or the caller cancels.
		select {
		case msg := <-t.responses:
			return t.responseFrame(req, msg)
		case <-req.Context().Done():
			return nil, req.Context().Err()
		}

	case strings.HasSuffix(path, "/SendMessage"):
		return t.handleSendMessage(req)

	case strings.HasSuffix(path, "/upload/finalize"):
		return t.handleMediaUploadFinalize(req)

	case strings.HasSuffix(path, "/upload"):
		if req.Method == http.MethodPost {
			return t.handleMediaUploadStart(req)
		}
		return t.handleMediaDownload(req)

	case strings.HasSuffix(path, "/AckMessages"):
		// Acks are fire-and-forget; acknowledge without a synthetic response.
		return protobufResponse(req, &gmproto.OutgoingRPCResponse{})

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

// PairingData is the fixture emitted on the first long-poll request to complete
// a simulated pairing. Fields are exported so tests can inspect them.
type PairingData struct {
	Mobile  *gmproto.Device
	Browser *gmproto.Device
	Token   *gmproto.TokenData
}

// DefaultPairingData returns the default simulated phone/browser pair.
func DefaultPairingData() PairingData {
	return PairingData{
		Mobile:  &gmproto.Device{SourceID: "emulator-phone", UserID: 1, Network: "Bugle"},
		Browser: &gmproto.Device{SourceID: "emulator-browser", UserID: 1, Network: "Bugle"},
		Token: &gmproto.TokenData{
			TachyonAuthToken: []byte("emulator-tachyon-token"),
			TTL:              86_400_000_000, // microseconds (24 hours)
		},
	}
}

// pairingResponse builds the long-poll stream that delivers a PairEvent.
//
// readLongPoll consumes the opening "[[" marker, then each pblite element until
// the JSON is complete, then the "]]" end marker. Each segment is returned by a
// distinct Read call so the framing stays intact.
func (t *Transport) pairingResponse(req *http.Request) (*http.Response, error) {
	pair := DefaultPairingData()
	pairBytes, err := proto.Marshal(&gmproto.RPCPairData{
		Event: &gmproto.RPCPairData_Paired{Paired: &gmproto.PairedData{
			Mobile:    pair.Mobile,
			Browser:   pair.Browser,
			TokenData: pair.Token,
		}},
	})
	if err != nil {
		return nil, fmt.Errorf("emulator (HARN-02): marshal pair event: %w", err)
	}

	element, err := pblite.Marshal(&gmproto.LongPollingPayload{
		Data: &gmproto.IncomingRPCMessage{
			ResponseID:  "emulator-pair-response",
			BugleRoute:  gmproto.BugleRoute_PairEvent,
			MessageData: pairBytes,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("emulator (HARN-02): marshal long-poll payload: %w", err)
	}

	return &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type": []string{"application/json+protobuf"},
		},
		Body: &segmentReader{segments: [][]byte{
			[]byte("[["),
			element,
			[]byte("]]"),
		}},
		Request: req,
	}, nil
}

// segmentReader serves each segment in its own Read call so a buffered reader
// cannot coalesce protocol markers with payload bytes.
type segmentReader struct {
	segments [][]byte
	index    int
}

func (r *segmentReader) Read(p []byte) (int, error) {
	if r.index >= len(r.segments) {
		return 0, io.EOF
	}
	seg := r.segments[r.index]
	n := copy(p, seg)
	if n < len(seg) {
		r.segments[r.index] = seg[n:]
	} else {
		r.index++
	}
	return n, nil
}

func (r *segmentReader) Close() error { return nil }
