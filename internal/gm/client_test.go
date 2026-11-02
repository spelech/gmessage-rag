package gm

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mau.fi/mautrix-gmessages/pkg/libgm"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
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
