package testing

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"time"

	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
	"google.golang.org/protobuf/proto"
)

// DisturbanceType defines injected network or protocol disturbances.
type DisturbanceType int

const (
	DisturbanceNone DisturbanceType = iota
	DisturbanceSocketDrop
	DisturbanceHTTPError
	DisturbanceCorruptPayload
	DisturbanceSlowResponse
)

// MockGMServer provides an in-memory Google Messages web backend simulation fixture (HARN-01).
// It serves synthetic protobuf events over HTTP/SSE, mimics /bind and /channel endpoints,
// and injects controls disturbances (disconnects, delays, corruptions) to verify consumer resilience.
type MockGMServer struct {
	mu           sync.RWMutex
	server       *httptest.Server
	clients      map[chan []byte]struct{}
	disturbance  DisturbanceType
	delay        time.Duration
	eventHistory [][]byte
}

// NewMockGMServer creates and starts a new mock Google Messages server.
func NewMockGMServer() *MockGMServer {
	s := &MockGMServer{
		clients:      make(map[chan []byte]struct{}),
		eventHistory: make([][]byte, 0),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/channel/bind", s.handleBind)
	mux.HandleFunc("/channel/events", s.handleEventsSSE)

	s.server = httptest.NewServer(mux)
	return s
}

// URL returns the mock server's root HTTP URL.
func (s *MockGMServer) URL() string {
	return s.server.URL
}

// Close shuts down the mock server and disconnects active listeners.
func (s *MockGMServer) Close() {
	s.mu.Lock()
	for ch := range s.clients {
		close(ch)
		delete(s.clients, ch)
	}
	s.mu.Unlock()

	s.server.Close()
}

// SetDisturbance configures an active disturbance mode.
func (s *MockGMServer) SetDisturbance(d DisturbanceType, delay time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.disturbance = d
	s.delay = delay
}

// ClearDisturbance removes any active disturbance.
func (s *MockGMServer) ClearDisturbance() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.disturbance = DisturbanceNone
	s.delay = 0
}

// EmitProtobuf broadcasts an arbitrary protobuf message to connected SSE listeners.
func (s *MockGMServer) EmitProtobuf(msg proto.Message) error {
	data, err := proto.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal protobuf: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.eventHistory = append(s.eventHistory, data)

	for ch := range s.clients {
		select {
		case ch <- data:
		default:
			// If buffer full, skip in synthetic simulation
		}
	}
	return nil
}

// EmitTextMessage helper crafts and emits an incoming or outgoing text message update.
func (s *MockGMServer) EmitTextMessage(msgID, convID, sender, text string, isOutgoing bool, timestamp time.Time) error {
	statusVal := gmproto.MessageStatusType_INCOMING_COMPLETE
	if isOutgoing {
		statusVal = gmproto.MessageStatusType_OUTGOING_COMPLETE
	}

	message := &gmproto.Message{
		MessageID:      msgID,
		ConversationID: convID,
		ParticipantID:  sender,
		MessageStatus: &gmproto.MessageStatus{
			Status: statusVal,
		},
		Timestamp: timestamp.UnixMicro(),
		MessageInfo: []*gmproto.MessageInfo{
			{
				Data: &gmproto.MessageInfo_MessageContent{
					MessageContent: &gmproto.MessageContent{
						Content: text,
					},
				},
			},
		},
	}

	event := &gmproto.UpdateEvents{
		Event: &gmproto.UpdateEvents_MessageEvent{
			MessageEvent: &gmproto.MessageEvent{
				Data: []*gmproto.Message{message},
			},
		},
	}

	return s.EmitProtobuf(event)
}

// EmitTypingEvent helper crafts and emits a typing indicator event (PIPE-05).
func (s *MockGMServer) EmitTypingEvent(convID, senderNumber string, isTyping bool) error {
	typingType := gmproto.TypingTypes_STOPPED_TYPING
	if isTyping {
		typingType = gmproto.TypingTypes_STARTED_TYPING
	}

	event := &gmproto.UpdateEvents{
		Event: &gmproto.UpdateEvents_TypingEvent{
			TypingEvent: &gmproto.TypingEvent{
				Data: &gmproto.TypingData{
					ConversationID: convID,
					Type:           typingType,
					User: &gmproto.User{
						Number: senderNumber,
					},
				},
			},
		},
	}
	return s.EmitProtobuf(event)
}

// History returns a copy of emitted raw protobuf payloads.
func (s *MockGMServer) History() [][]byte {
	s.mu.RLock()
	defer s.mu.RUnlock()
	res := make([][]byte, len(s.eventHistory))
	copy(res, s.eventHistory)
	return res
}

func (s *MockGMServer) handleBind(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	dist := s.disturbance
	delay := s.delay
	s.mu.RUnlock()

	if delay > 0 {
		time.Sleep(delay)
	}

	if dist == DisturbanceHTTPError {
		http.Error(w, "synthetic 503 service unavailable", http.StatusServiceUnavailable)
		return
	}

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("OK"))
}

func (s *MockGMServer) handleEventsSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	s.mu.RLock()
	dist := s.disturbance
	s.mu.RUnlock()

	if dist == DisturbanceSocketDrop {
		hj, ok := w.(http.Hijacker)
		if ok {
			conn, _, _ := hj.Hijack()
			if conn != nil {
				_ = conn.Close()
				return
			}
		}
		http.Error(w, "socket dropped", http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	msgChan := make(chan []byte, 32)
	s.mu.Lock()
	s.clients[msgChan] = struct{}{}
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.clients, msgChan)
		s.mu.Unlock()
	}()

	notify := r.Context().Done()
	for {
		select {
		case <-notify:
			return
		case data, ok := <-msgChan:
			if !ok {
				return
			}
			s.mu.RLock()
			currentDist := s.disturbance
			s.mu.RUnlock()

			encoded := base64.StdEncoding.EncodeToString(data)
			if currentDist == DisturbanceCorruptPayload {
				encoded = "NOT_VALID_PROTOBUF_DATA_%%"
			}

			_, _ = fmt.Fprintf(w, "data: %s\n\n", encoded)
			flusher.Flush()
		}
	}
}

var _ context.Context = context.TODO()
