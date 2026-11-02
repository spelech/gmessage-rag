package testing

import (
	"bufio"
	"context"
	"encoding/base64"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
	"google.golang.org/protobuf/proto"
)

func TestMockGMServer_LifecycleAndEmission(t *testing.T) {
	server := NewMockGMServer()
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", server.URL()+"/channel/events", nil)
	require.NoError(t, err)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))

	// Emit a text message
	now := time.Now()
	err = server.EmitTextMessage("msg-101", "conv-alpha", "+15550199", "Test simulation payload", false, now)
	require.NoError(t, err)

	reader := bufio.NewReader(resp.Body)
	line, err := reader.ReadString('\n')
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(line, "data: "))

	rawPayload := strings.TrimPrefix(line, "data: ")
	rawPayload = strings.TrimSpace(rawPayload)

	decoded, err := base64.StdEncoding.DecodeString(rawPayload)
	require.NoError(t, err)

	var updateEvents gmproto.UpdateEvents
	err = proto.Unmarshal(decoded, &updateEvents)
	require.NoError(t, err)

	msgEvent := updateEvents.GetMessageEvent()
	require.NotNil(t, msgEvent)
	require.Len(t, msgEvent.Data, 1)

	msg := msgEvent.Data[0]
	assert.Equal(t, "msg-101", msg.GetMessageID())
	assert.Equal(t, "conv-alpha", msg.GetConversationID())
	require.NotEmpty(t, msg.GetMessageInfo())
	assert.Equal(t, "Test simulation payload", msg.GetMessageInfo()[0].GetMessageContent().GetContent())
}

func TestMockGMServer_Disturbance_HTTPErrorAndCorruptPayload(t *testing.T) {
	server := NewMockGMServer()
	defer server.Close()

	// 1. Test HTTP error disturbance on bind
	server.SetDisturbance(DisturbanceHTTPError, 0)
	resp, err := http.Get(server.URL() + "/channel/bind")
	require.NoError(t, err)
	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
	_ = resp.Body.Close()

	server.ClearDisturbance()
	resp, err = http.Get(server.URL() + "/channel/bind")
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	_ = resp.Body.Close()

	// 2. Test Corrupt payload disturbance
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", server.URL()+"/channel/events", nil)
	require.NoError(t, err)

	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	server.SetDisturbance(DisturbanceCorruptPayload, 0)
	err = server.EmitTypingEvent("conv-beta", "+15550100", true)
	require.NoError(t, err)

	reader := bufio.NewReader(resp.Body)
	line, err := reader.ReadString('\n')
	require.NoError(t, err)

	rawPayload := strings.TrimSpace(strings.TrimPrefix(line, "data: "))
	decoded, err := base64.StdEncoding.DecodeString(rawPayload)
	if err == nil {
		var updateEvents gmproto.UpdateEvents
		err = proto.Unmarshal(decoded, &updateEvents)
	}
	assert.Error(t, err, "corrupt payload must fail base64 decode or proto unmarshal")
}

func TestMockGMServer_ConcurrencyAndRaceSafety(t *testing.T) {
	server := NewMockGMServer()
	defer server.Close()

	var wg sync.WaitGroup
	// Emit concurrent messages while setting disturbances
	for i := 0; i < 25; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_ = server.EmitTextMessage(
				"msg-stress",
				"conv-stress",
				"+15550000",
				"concurrent message",
				false,
				time.Now(),
			)
		}(i)
	}

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			server.SetDisturbance(DisturbanceSlowResponse, 5*time.Millisecond)
			time.Sleep(10 * time.Millisecond)
			server.ClearDisturbance()
			_ = server.History()
		}()
	}

	wg.Wait()
}
