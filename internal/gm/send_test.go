package gm

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/spelech/gmessage-rag/internal/gm/testing/emulator"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mau.fi/mautrix-gmessages/pkg/libgm"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
)

// pairedEmulatorClient seeds a paired session and routes it through the
// in-process emulator. Seeding the session (instead of driving StartLogin)
// avoids the post-pairing reconnect window where no long-poll receiver is
// active yet.
func pairedEmulatorClient(t *testing.T) (*Client, *emulator.Transport) {
	t.Helper()

	emu := emulator.NewTransport()
	// The session is already paired; skip the synthetic PairEvent.
	emu.SetPaired(true)

	sessionPath := filepath.Join(t.TempDir(), "session.json")
	auth := libgm.NewAuthData()
	auth.Browser = &gmproto.Device{SourceID: "emulator-browser", UserID: 1, Network: "Bugle"}
	auth.Mobile = &gmproto.Device{SourceID: "emulator-phone", UserID: 1, Network: "Bugle"}
	auth.TachyonAuthToken = []byte("emulator-tachyon-token")
	auth.TachyonExpiry = time.Now().Add(24 * time.Hour)
	require.NoError(t, SaveAuthData(sessionPath, auth))

	client, err := NewClient(ClientOptions{
		SessionPath:        sessionPath,
		HTTPClientSettings: emu.Settings(),
	})
	require.NoError(t, err)
	require.True(t, client.IsLoggedIn(), "seeded session should be logged in")

	// Sends are acknowledged synchronously in these tests; skip the echo settle.
	client.SetSendSettle(0)

	// The emulator needs the session cipher to decode and encrypt RPC payloads.
	emu.SetRequestCipher(client.AuthData().RequestCrypto)

	return client, emu
}

func TestSendText_Emulator(t *testing.T) {
	client, emu := pairedEmulatorClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	res, err := client.SendText(ctx, OutgoingMessage{
		ConversationID: "conv-send-text",
		Text:           "hello emulator",
		ReplyToID:      "reply-1",
	})
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, "conv-send-text", res.ConversationID)
	assert.Equal(t, "SUCCESS", res.Status)
	assert.NotEmpty(t, res.TmpID)

	assert.Contains(t, emu.Requests(), "/$rpc/google.internal.communications.instantmessaging.v1.Messaging/SendMessage",
		"emulator should have received the send RPC")
}

func TestSendText_Emulator_ValidatesInput(t *testing.T) {
	client, _ := pairedEmulatorClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := client.SendText(ctx, OutgoingMessage{ConversationID: "", Text: "hello"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "conversation id is required")

	_, err = client.SendText(ctx, OutgoingMessage{ConversationID: "conv", Text: "   "})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "text is required")
}

func TestSendMedia_Emulator(t *testing.T) {
	client, emu := pairedEmulatorClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	res, err := client.SendMedia(ctx, OutgoingMedia{
		ConversationID: "conv-send-media",
		FileName:       "photo.jpg",
		MimeType:       "image/jpeg",
		Data:           []byte("fake-image-bytes"),
		Caption:        "a caption",
	})
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, "conv-send-media", res.ConversationID)
	assert.Equal(t, "SUCCESS", res.Status)
	assert.NotEmpty(t, res.MediaID)

	assert.Contains(t, emu.Requests(), "/$rpc/google.internal.communications.instantmessaging.v1.Messaging/SendMessage")
}

func TestSendMedia_Emulator_ValidatesInput(t *testing.T) {
	client, _ := pairedEmulatorClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := client.SendMedia(ctx, OutgoingMedia{ConversationID: "conv"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "media data is required")
}

func TestSendReaction_Emulator(t *testing.T) {
	client, emu := pairedEmulatorClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := client.SendReaction(ctx, "message-123", "👍", false, nil)
	require.NoError(t, err)

	assert.Contains(t, emu.Requests(), "/$rpc/google.internal.communications.instantmessaging.v1.Messaging/SendMessage",
		"emulator should have received the reaction RPC")
}

func TestSendReaction_Emulator_Remove(t *testing.T) {
	client, _ := pairedEmulatorClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := client.SendReaction(ctx, "message-123", "👍", true, nil)
	require.NoError(t, err)
}

func TestSendReaction_Emulator_ValidatesInput(t *testing.T) {
	client, _ := pairedEmulatorClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := client.SendReaction(ctx, "", "👍", false, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "message id is required")

	err = client.SendReaction(ctx, "message-123", "", false, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "emoji is required")
}
