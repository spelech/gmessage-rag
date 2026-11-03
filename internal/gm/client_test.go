package gm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mau.fi/mautrix-gmessages/pkg/libgm"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/events"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
	exhttp "go.mau.fi/util/exhttp"
)

// TestNormalizeTimestamp validates timestamp conversion across microseconds (> 10^14), milliseconds (> 10^11), and seconds.
func TestNormalizeTimestamp(t *testing.T) {
	// Reference UTC time: 2026-11-01 12:00:00 UTC
	refTime := time.Date(2026, time.November, 1, 12, 0, 0, 0, time.UTC)
	refUnixSec := refTime.Unix() // 1793534400
	refUnixMilli := refUnixSec * 1_000
	refUnixMicro := refUnixSec * 1_000_000

	// 1. Microseconds (> 10^14)
	tsMicro := refUnixMicro + 123456
	normMicro := NormalizeTimestamp(tsMicro)
	assert.Equal(t, time.UTC, normMicro.Location())
	assert.Equal(t, refUnixSec, normMicro.Unix())
	assert.Equal(t, 123456000, normMicro.Nanosecond())

	// 2. Milliseconds (> 10^11)
	tsMilli := refUnixMilli + 789
	normMilli := NormalizeTimestamp(tsMilli)
	assert.Equal(t, time.UTC, normMilli.Location())
	assert.Equal(t, refUnixSec, normMilli.Unix())
	assert.Equal(t, 789000000, normMilli.Nanosecond())

	// 3. Seconds (<= 10^11)
	tsSec := refUnixSec
	normSec := NormalizeTimestamp(tsSec)
	assert.Equal(t, time.UTC, normSec.Location())
	assert.Equal(t, refUnixSec, normSec.Unix())
	assert.Equal(t, 0, normSec.Nanosecond())

	// 4. Edge cases: zero or negative
	now := time.Now().UTC()
	normZero := NormalizeTimestamp(0)
	assert.WithinDuration(t, now, normZero, 2*time.Second)
	assert.Equal(t, time.UTC, normZero.Location())
}

// TestResolveSenderDirection validates status code direction mapping:
// 0 <= status < 100 (OUTGOING_*) -> is_from_me = true
// 100 <= status < 200 (INCOMING_*) -> is_from_me = false
func TestResolveSenderDirection(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int32
		wantFromMe bool
		wantOk     bool
	}{
		// Outgoing boundary & values
		{"OUTGOING_COMPLETE 1", 1, true, true},
		{"OUTGOING_DELIVERED 2", 2, true, true},
		{"OUTGOING_DISPLAYED 11", 11, true, true},
		{"OUTGOING lower bound 0", 0, true, true},
		{"OUTGOING upper bound 99", 99, true, true},

		// Incoming boundary & values
		{"INCOMING_COMPLETE 100", 100, false, true},
		{"INCOMING_DELIVERED 108", 108, false, true},
		{"INCOMING_DISPLAYED 109", 109, false, true},
		{"INCOMING upper bound 199", 199, false, true},

		// Tombstones / Other
		{"TOMBSTONE 200", 200, false, false},
		{"DELETED 300", 300, false, false},
		{"Negative -1", -1, false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isFromMe, ok := ResolveSenderDirection(tt.statusCode)
			assert.Equal(t, tt.wantOk, ok)
			if ok {
				assert.Equal(t, tt.wantFromMe, isFromMe)
			}
		})
	}

	// Also verify IsFromMe helper with typed enum
	assert.True(t, IsFromMe(gmproto.MessageStatusType_OUTGOING_DELIVERED))
	assert.True(t, IsFromMe(gmproto.MessageStatusType_OUTGOING_COMPLETE))
	assert.False(t, IsFromMe(gmproto.MessageStatusType_INCOMING_COMPLETE))
	assert.False(t, IsFromMe(gmproto.MessageStatusType_INCOMING_DELIVERED))
}

// TestClient_MessageEventDispatch tests event handling dispatching to message upsert and debouncer callbacks.
func TestClient_MessageEventDispatch(t *testing.T) {
	tempDir := t.TempDir()
	sessionPath := filepath.Join(tempDir, "session.json")

	var debouncedConvID string
	var debouncedMu sync.Mutex

	var upsertedMsg *gmproto.Message
	var upsertedFromMe bool
	var upsertedTime time.Time
	var upsertMu sync.Mutex

	client, err := NewClient(ClientOptions{
		SessionPath: sessionPath,
		OnConversationActive: func(convID string) {
			debouncedMu.Lock()
			debouncedConvID = convID
			debouncedMu.Unlock()
		},
		OnMessageUpsert: func(ctx context.Context, msg *gmproto.Message, isFromMe bool, sentAt time.Time) error {
			upsertMu.Lock()
			upsertedMsg = msg
			upsertedFromMe = isFromMe
			upsertedTime = sentAt
			upsertMu.Unlock()
			return nil
		},
	})
	require.NoError(t, err)

	testTS := int64(1700000000000000) // microsecond timestamp
	rawMsg := &gmproto.Message{
		MessageID:      "msg-12345",
		ConversationID: "conv-999",
		Timestamp:      testTS,
		MessageStatus: &gmproto.MessageStatus{
			Status: gmproto.MessageStatusType_OUTGOING_DELIVERED,
		},
	}

	wrapped := &libgm.WrappedMessage{
		Message: rawMsg,
		IsOld:   false,
	}

	client.handleEvent(wrapped)

	debouncedMu.Lock()
	assert.Equal(t, "conv-999", debouncedConvID)
	debouncedMu.Unlock()

	upsertMu.Lock()
	require.NotNil(t, upsertedMsg)
	assert.Equal(t, "msg-12345", upsertedMsg.GetMessageID())
	assert.True(t, upsertedFromMe)
	assert.Equal(t, NormalizeTimestamp(testTS), upsertedTime)
	upsertMu.Unlock()

	// Test incoming message
	rawMsgIn := &gmproto.Message{
		MessageID:      "msg-incoming",
		ConversationID: "conv-100",
		Timestamp:      int64(1700000000000), // millisecond timestamp
		MessageStatus: &gmproto.MessageStatus{
			Status: gmproto.MessageStatusType_INCOMING_COMPLETE,
		},
	}
	client.handleEvent(rawMsgIn)

	debouncedMu.Lock()
	assert.Equal(t, "conv-100", debouncedConvID)
	debouncedMu.Unlock()

	upsertMu.Lock()
	assert.Equal(t, "msg-incoming", upsertedMsg.GetMessageID())
	assert.False(t, upsertedFromMe)
	upsertMu.Unlock()
}

// [Requirement(PIPE-05)]
// TestClient_TypingEventDispatch proves the client forwards participant typing
// indicators to the action-aware debouncer callback (PIPE-05).
func TestClient_TypingEventDispatch(t *testing.T) {
	tempDir := t.TempDir()
	sessionPath := filepath.Join(tempDir, "session.json")

	var mu sync.Mutex
	var gotConvID string
	var gotType gmproto.TypingTypes
	var calls int

	client, err := NewClient(ClientOptions{
		SessionPath: sessionPath,
		OnTypingActivity: func(conversationID string, typingType gmproto.TypingTypes) {
			mu.Lock()
			gotConvID = conversationID
			gotType = typingType
			calls++
			mu.Unlock()
		},
	})
	require.NoError(t, err)

	// A started-typing event must surface the conversation and type verbatim.
	client.handleEvent(&gmproto.TypingData{
		ConversationID: "conv-typing-dispatch",
		Type:           gmproto.TypingTypes_STARTED_TYPING,
	})

	mu.Lock()
	assert.Equal(t, "conv-typing-dispatch", gotConvID)
	assert.Equal(t, gmproto.TypingTypes_STARTED_TYPING, gotType)
	assert.Equal(t, 1, calls)
	mu.Unlock()

	// A stopped-typing event is forwarded too, so the debouncer can distinguish it.
	client.handleEvent(&gmproto.TypingData{
		ConversationID: "conv-typing-dispatch",
		Type:           gmproto.TypingTypes_STOPPED_TYPING,
	})

	mu.Lock()
	assert.Equal(t, gmproto.TypingTypes_STOPPED_TYPING, gotType)
	assert.Equal(t, 2, calls)
	mu.Unlock()

	// Events without a conversation ID, or with no callback configured, are no-ops.
	client.handleEvent(&gmproto.TypingData{Type: gmproto.TypingTypes_STARTED_TYPING})
	client.handleEvent((*gmproto.TypingData)(nil))

	mu.Lock()
	assert.Equal(t, 2, calls, "empty-conversation and nil typing events must not invoke the callback")
	mu.Unlock()

	// A client without a typing callback must not panic.
	plain, err := NewClient(ClientOptions{SessionPath: sessionPath})
	require.NoError(t, err)
	plain.handleEvent(&gmproto.TypingData{
		ConversationID: "conv-no-cb",
		Type:           gmproto.TypingTypes_STARTED_TYPING,
	})
}

func TestClient_AccessorsAndOptions(t *testing.T) {
	tempDir := t.TempDir()
	sessionPath := filepath.Join(tempDir, "session.json")

	// 1. Nominal with sessionPath and HTTPClientSettings
	httpSettings := &exhttp.ClientSettings{}
	client, err := NewClient(ClientOptions{
		SessionPath:        sessionPath,
		HTTPClientSettings: httpSettings,
	})
	require.NoError(t, err)
	assert.NotNil(t, client.LibGM())
	assert.NotNil(t, client.AuthData())
	assert.Equal(t, sessionPath, client.SessionPath())

	// 2. Default session path when empty
	clientDef, err := NewClient(ClientOptions{
		SessionPath: "",
	})
	require.NoError(t, err)
	assert.Equal(t, DefaultSessionPath, clientDef.SessionPath())

	// 3. Error when auth data file is corrupted JSON
	corruptPath := filepath.Join(tempDir, "corrupt.json")
	require.NoError(t, os.WriteFile(corruptPath, []byte("invalid-json-data"), 0600))
	_, err = NewClient(ClientOptions{
		SessionPath: corruptPath,
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to load auth data")
}

func TestClient_ConnectAndReconnectLoop(t *testing.T) {
	tempDir := t.TempDir()
	sessionPath := filepath.Join(tempDir, "session.json")
	client, err := NewClient(ClientOptions{
		SessionPath: sessionPath,
	})
	require.NoError(t, err)

	// Connect should fail when not logged in
	ctx := context.Background()
	err = client.Connect(ctx)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cannot connect: client is not logged in / paired")

	// ReconnectLoop should return when context is canceled
	cancelCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	client.ReconnectLoop(cancelCtx)
}

func TestClient_FetchMessages(t *testing.T) {
	tempDir := t.TempDir()
	sessionPath := filepath.Join(tempDir, "session.json")
	client, err := NewClient(ClientOptions{
		SessionPath: sessionPath,
	})
	require.NoError(t, err)

	ctx := context.Background()
	// FetchMessages without cursor
	_, err = client.FetchMessages(ctx, "conv-1", 10, nil)
	assert.Error(t, err) // Not logged in / connected

	// FetchMessages with cursor
	_, err = client.FetchMessages(ctx, "conv-1", 10, []byte("cursor-abc"))
	assert.Error(t, err)
}

func TestClient_EventHandling_AllBranches(t *testing.T) {
	tempDir := t.TempDir()
	sessionPath := filepath.Join(tempDir, "session.json")
	var upsertCallCount int
	var upsertErr error

	client, err := NewClient(ClientOptions{
		SessionPath: sessionPath,
		OnMessageUpsert: func(ctx context.Context, msg *gmproto.Message, isFromMe bool, sentAt time.Time) error {
			upsertCallCount++
			return upsertErr
		},
	})
	require.NoError(t, err)

	// 1. PairSuccessful event
	client.handleEvent(&events.PairSuccessful{PhoneID: "phone-test-123"})
	select {
	case <-client.pairDone:
		// channel was closed as expected
	default:
		t.Errorf("expected pairDone channel to be closed")
	}
	// Calling again should not panic on already-closed channel
	client.handleEvent(&events.PairSuccessful{PhoneID: "phone-test-123"})

	// 2. AuthTokenRefreshed event
	client.handleEvent(&events.AuthTokenRefreshed{})

	// 3. ListenFatalError event
	fatalErr := errors.New("simulated fatal listener error")
	client.handleEvent(&events.ListenFatalError{Error: fatalErr})
	client.mu.RLock()
	assert.Equal(t, fatalErr, client.lastPairError)
	client.mu.RUnlock()

	// 4. Default / unknown event
	client.handleEvent("unknown-string-event")

	// 5. handleIncomingMessage(nil)
	client.handleIncomingMessage(nil)

	// 6. Failing OnMessageUpsert
	upsertErr = errors.New("upsert failure")
	dummyMsg := &gmproto.Message{
		MessageID: "msg-err-01",
	}
	client.handleIncomingMessage(dummyMsg)
	assert.Equal(t, 1, upsertCallCount)
}

// [Requirement(TRANS-04)] [Requirement(AUTH-03)]
// TestClient_WatchdogEventsDispatch validates that PhoneNotResponding, PhoneRespondingAgain,
// and ListenFatalError events trigger configured watchdog callbacks.
func TestClient_WatchdogEventsDispatch(t *testing.T) {
	tempDir := t.TempDir()
	sessionPath := filepath.Join(tempDir, "session.json")

	var (
		mu                 sync.Mutex
		phoneNotResponding int
		phoneResponding    int
		fatalErrReceived   error
	)

	client, err := NewClient(ClientOptions{
		SessionPath: sessionPath,
		OnPhoneNotResponding: func() {
			mu.Lock()
			phoneNotResponding++
			mu.Unlock()
		},
		OnPhoneRespondingAgain: func() {
			mu.Lock()
			phoneResponding++
			mu.Unlock()
		},
		OnListenFatalError: func(err error) {
			mu.Lock()
			fatalErrReceived = err
			mu.Unlock()
		},
	})
	require.NoError(t, err)

	// 1. PhoneNotResponding dispatch
	client.handleEvent(&events.PhoneNotResponding{})
	mu.Lock()
	assert.Equal(t, 1, phoneNotResponding)
	mu.Unlock()

	// 2. PhoneRespondingAgain dispatch
	client.handleEvent(&events.PhoneRespondingAgain{})
	mu.Lock()
	assert.Equal(t, 1, phoneResponding)
	mu.Unlock()

	// 3. ListenFatalError dispatch
	expectedFatal := errors.New("unrecoverable stream closure")
	client.handleEvent(&events.ListenFatalError{Error: expectedFatal})
	mu.Lock()
	assert.Equal(t, expectedFatal, fatalErrReceived)
	mu.Unlock()

	// 4. Client with nil callbacks must handle events without panic
	plainClient, err := NewClient(ClientOptions{
		SessionPath: sessionPath,
	})
	require.NoError(t, err)

	assert.NotPanics(t, func() {
		plainClient.handleEvent(&events.PhoneNotResponding{})
		plainClient.handleEvent(&events.PhoneRespondingAgain{})
		plainClient.handleEvent(&events.ListenFatalError{Error: errors.New("nil-cb error")})
	})
}
