package testing

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/spelech/gmessage-rag/internal/gm"
	"github.com/spelech/gmessage-rag/internal/gm/testing/emulator"
	"github.com/spelech/gmessage-rag/internal/pipeline"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gmcrypto "go.mau.fi/mautrix-gmessages/pkg/libgm/crypto"
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

// connectedEmulatedClient builds the real gm.Client wired to the mock server's
// in-process emulator transport, seeds a logged-in session, and connects. It
// exercises the production client and ingestion seams, never a mock.
func connectedEmulatedClient(t *testing.T, server *MockGMServer) *gm.Client {
	t.Helper()

	client, err := gm.NewClient(gm.ClientOptions{
		SessionPath:        filepath.Join(t.TempDir(), "session.json"),
		Logger:             zerolog.Nop(),
		HTTPClientSettings: server.Settings(),
	})
	require.NoError(t, err)

	auth := client.AuthData()
	auth.RequestCrypto = gmcrypto.NewAESCTRHelper()
	auth.Browser = &gmproto.Device{SourceID: "emulator-browser", UserID: 1, Network: "Bugle"}
	auth.Mobile = &gmproto.Device{SourceID: "emulator-phone", UserID: 1, Network: "Bugle"}
	auth.TachyonAuthToken = []byte("emulator-tachyon-token")
	auth.TachyonExpiry = time.Now().Add(24 * time.Hour)

	server.SetPaired(true)
	server.SetRequestCipher(auth.RequestCrypto)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	require.NoError(t, client.LibGM().Connect(ctx))

	return client
}

func syntheticTextMessage(messageID, conversationID, body string) *gmproto.Message {
	return &gmproto.Message{
		MessageID:      messageID,
		ConversationID: conversationID,
		ParticipantID:  "+15550001",
		MessageStatus: &gmproto.MessageStatus{
			Status: gmproto.MessageStatusType_INCOMING_COMPLETE,
		},
		Timestamp: time.Now().UnixMicro(),
		MessageInfo: []*gmproto.MessageInfo{
			{
				Data: &gmproto.MessageInfo_MessageContent{
					MessageContent: &gmproto.MessageContent{Content: body},
				},
			},
		},
	}
}

// [Requirement(HARN-02)]
// TestMockServer_FetchMessages drives the real gm.Client through the fixture's
// emulated LIST_MESSAGES RPC and asserts observable paged message outcomes.
func TestMockServer_FetchMessages(t *testing.T) {
	server := NewMockGMServer()
	defer server.Close()

	client := connectedEmulatedClient(t, server)

	const convID = "conv-harn02-fetch"
	firstPage := []*gmproto.Message{
		syntheticTextMessage("m-001", convID, "hello one"),
		syntheticTextMessage("m-002", convID, "hello two"),
	}
	secondPage := []*gmproto.Message{
		syntheticTextMessage("m-003", convID, "hello three"),
	}
	server.SetMessagePages(convID, map[string]emulator.MessagePage{
		"":           {Messages: firstPage, NextCursor: "cursor-002"},
		"cursor-002": {Messages: secondPage},
	})

	etl := &pipeline.ETLPipeline{Fetcher: client}
	ctx := context.Background()

	page1, cursor1, err := etl.Extract(ctx, convID, 2, nil)
	require.NoError(t, err)
	require.Len(t, page1, 2)
	assert.Equal(t, "m-001", page1[0].GetMessageID())
	assert.Equal(t, "hello one", page1[0].GetMessageInfo()[0].GetMessageContent().GetContent())
	assert.Equal(t, []byte("cursor-002"), cursor1)

	page2, cursor2, err := etl.Extract(ctx, convID, 2, cursor1)
	require.NoError(t, err)
	require.Len(t, page2, 1)
	assert.Equal(t, "m-003", page2[0].GetMessageID())
	assert.Empty(t, cursor2, "final page must terminate pagination")

	transformed := etl.Transform(page1, convID)
	require.Len(t, transformed, 2)
	assert.Equal(t, "hello two", transformed[1].Body)

	assert.Contains(t, server.emu.Requests(),
		"/$rpc/google.internal.communications.instantmessaging.v1.Messaging/SendMessage")
}

// [Requirement(HARN-02)]
// TestMockServer_ListContacts drives the real gm.Client through the fixture's
// emulated LIST_CONTACTS RPC and asserts observable normalized contacts.
func TestMockServer_ListContacts(t *testing.T) {
	server := NewMockGMServer()
	defer server.Close()

	client := connectedEmulatedClient(t, server)

	server.SetContacts([]*gmproto.Contact{
		{
			ContactID:     "c-1",
			ParticipantID: "p-1",
			Name:          "Alice Smith",
			Number:        &gmproto.ContactNumber{Number: "+15551230001"},
		},
		{
			ContactID:     "c-2",
			ParticipantID: "p-2",
			NameAgain:     "Bob Jones",
			Number:        &gmproto.ContactNumber{Number: "+15551230002"},
		},
	})

	summaries, err := gm.SyncContacts(context.Background(), client)
	require.NoError(t, err)
	require.Len(t, summaries, 2)

	assert.Equal(t, "c-1", summaries[0].ContactID)
	assert.Equal(t, "Alice Smith", summaries[0].Name)
	assert.Equal(t, "+15551230001", summaries[0].Number)
	assert.Equal(t, "Bob Jones", summaries[1].Name)
	assert.Equal(t, "+15551230002", summaries[1].Number)
}

// [Requirement(HARN-02)]
// TestMockServer_MediaDownload proves the emulated /upload endpoint streams
// encrypted media that the real media ingestion decrypts and persists.
func TestMockServer_MediaDownload(t *testing.T) {
	server := NewMockGMServer()
	defer server.Close()

	client := connectedEmulatedClient(t, server)

	key := gmcrypto.GenerateKey(32)
	plaintext := []byte("synthetic image bytes for HARN-02 media download")
	server.SetMedia("media-1", plaintext, key)

	msg := &gmproto.Message{
		MessageID:      "m-media",
		ConversationID: "conv-harn02-media",
		MessageInfo: []*gmproto.MessageInfo{
			{
				Data: &gmproto.MessageInfo_MediaContent{
					MediaContent: &gmproto.MediaContent{
						MediaID:       "media-1",
						DecryptionKey: key,
						MimeType:      "image/png",
						MediaName:     "pic.png",
					},
				},
			},
		},
	}

	baseDir := t.TempDir()
	attachments, err := gm.ExtractAndSaveMedia(context.Background(), client, msg, baseDir)
	require.NoError(t, err)
	require.Len(t, attachments, 1)

	saved, err := os.ReadFile(filepath.Join(baseDir, attachments[0].RelativePath))
	require.NoError(t, err)
	assert.Equal(t, plaintext, saved)

	expectedHash := sha256.Sum256(plaintext)
	assert.Equal(t, hex.EncodeToString(expectedHash[:]), attachments[0].SHA256Hash)
}

// [Requirement(HARN-01)]
// TestMockServer_DisturbanceFailsFetch proves the disturbance fixture is consumed
// by real libgm ingestion: an injected 503 must surface through gm.Client.FetchMessages.
func TestMockServer_DisturbanceFailsFetch(t *testing.T) {
	server := NewMockGMServer()
	defer server.Close()

	client := connectedEmulatedClient(t, server)

	const convID = "conv-harn01-disturbance"
	server.SetMessagePages(convID, map[string]emulator.MessagePage{
		"": {Messages: []*gmproto.Message{syntheticTextMessage("m-dist", convID, "disturbed")}},
	})

	server.SetDisturbance(DisturbanceHTTPError, 0)
	defer server.ClearDisturbance()

	_, err := client.FetchMessages(context.Background(), convID, 1, nil)
	require.Error(t, err, "injected disturbance must surface to the ingestion path")
	assert.Contains(t, err.Error(), "503")
}
