package emulator

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	gmcrypto "go.mau.fi/mautrix-gmessages/pkg/libgm/crypto"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
	"go.mau.fi/util/pblite"
	"google.golang.org/protobuf/proto"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// passthroughCipher lets the emulator read plaintext test payloads, standing in
// for the paired session cipher.
type passthroughCipher struct{}

func (passthroughCipher) Encrypt(p []byte) ([]byte, error) { return append([]byte(nil), p...), nil }
func (passthroughCipher) Decrypt(p []byte) ([]byte, error) { return append([]byte(nil), p...), nil }

// [Requirement(HARN-02)]
func TestTransport_BuildRPCResponse_ListMessagesPaging(t *testing.T) {
	tr := NewTransport()
	tr.SetRequestCipher(passthroughCipher{})
	tr.SetMessagePages("conv-1", map[string]MessagePage{
		"":         {Messages: []*gmproto.Message{{MessageID: "m1"}}, NextCursor: "cursor-2"},
		"cursor-2": {Messages: []*gmproto.Message{{MessageID: "m2"}}},
	})

	firstReq, err := proto.Marshal(&gmproto.ListMessagesRequest{ConversationID: "conv-1"})
	require.NoError(t, err)
	first, ok := tr.buildRPCResponse(gmproto.ActionType_LIST_MESSAGES, firstReq).(*gmproto.ListMessagesResponse)
	require.True(t, ok)
	require.Len(t, first.GetMessages(), 1)
	assert.Equal(t, "m1", first.GetMessages()[0].GetMessageID())
	require.NotNil(t, first.GetCursor())
	assert.Equal(t, "cursor-2", first.GetCursor().GetLastItemID())

	// Following the cursor returns the terminal page with no next cursor.
	secondReq, err := proto.Marshal(&gmproto.ListMessagesRequest{
		ConversationID: "conv-1",
		Cursor:         &gmproto.Cursor{LastItemID: "cursor-2"},
	})
	require.NoError(t, err)
	second := tr.buildRPCResponse(gmproto.ActionType_LIST_MESSAGES, secondReq).(*gmproto.ListMessagesResponse)
	require.Len(t, second.GetMessages(), 1)
	assert.Equal(t, "m2", second.GetMessages()[0].GetMessageID())
	assert.Nil(t, second.GetCursor())

	// An unknown cursor yields an empty page rather than an error.
	unknownReq, err := proto.Marshal(&gmproto.ListMessagesRequest{
		ConversationID: "conv-1",
		Cursor:         &gmproto.Cursor{LastItemID: "missing"},
	})
	require.NoError(t, err)
	unknown := tr.buildRPCResponse(gmproto.ActionType_LIST_MESSAGES, unknownReq).(*gmproto.ListMessagesResponse)
	assert.Empty(t, unknown.GetMessages())
}

// [Requirement(HARN-02)]
func TestTransport_BuildRPCResponse_ContactsAndDefault(t *testing.T) {
	tr := NewTransport()
	tr.SetContacts([]*gmproto.Contact{{ContactID: "c1", Name: "Alice"}})

	contacts, ok := tr.buildRPCResponse(gmproto.ActionType_LIST_CONTACTS, nil).(*gmproto.ListContactsResponse)
	require.True(t, ok)
	require.Len(t, contacts.GetContacts(), 1)
	assert.Equal(t, "Alice", contacts.GetContacts()[0].GetName())

	def, ok := tr.buildRPCResponse(gmproto.ActionType_IS_BUGLE_DEFAULT, nil).(*gmproto.IsBugleDefaultResponse)
	require.True(t, ok)
	assert.True(t, def.GetSuccess())

	// Actions with no synthetic response return nil (fire-and-forget).
	assert.Nil(t, tr.buildRPCResponse(gmproto.ActionType_GET_CONVERSATION, nil))
}

// [Requirement(HARN-02)]
func TestTransport_DecryptIntoWithoutCipherIsNoop(t *testing.T) {
	tr := NewTransport()
	var req gmproto.ListMessagesRequest
	tr.decryptInto([]byte("not-decryptable"), &req)
	assert.Empty(t, req.GetConversationID())
}

// [Requirement(HARN-02)]
func TestTransport_EnqueueWithoutCipherFails(t *testing.T) {
	tr := NewTransport()
	err := tr.enqueueResponse("req-1", gmproto.ActionType_LIST_CONTACTS, &gmproto.ListContactsResponse{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no request cipher")
}

// [Requirement(HARN-02)]
func TestTransport_EnqueueAndLongPollDelivery(t *testing.T) {
	tr := NewTransport()
	tr.SetPaired(true)
	tr.SetRequestCipher(passthroughCipher{})

	require.NoError(t, tr.enqueueResponse("req-1", gmproto.ActionType_LIST_CONTACTS,
		&gmproto.ListContactsResponse{Contacts: []*gmproto.Contact{{ContactID: "c1"}}}))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req := httptest.NewRequest(http.MethodPost, "https://gm/$rpc/ReceiveMessages", nil).WithContext(ctx)
	resp, err := tr.RoundTrip(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Contains(t, string(body), "[[")
	assert.Contains(t, string(body), "]]")
}

// [Requirement(HARN-02)]
func TestTransport_HandleSendMessageQueuesResponse(t *testing.T) {
	tr := NewTransport()
	tr.SetRequestCipher(passthroughCipher{})
	tr.SetMessagePages("conv-1", map[string]MessagePage{
		"": {Messages: []*gmproto.Message{{MessageID: "m1"}}},
	})

	innerBytes, err := proto.Marshal(&gmproto.ListMessagesRequest{ConversationID: "conv-1"})
	require.NoError(t, err)
	dataBytes, err := proto.Marshal(&gmproto.OutgoingRPCData{
		RequestID:          "req-1",
		Action:             gmproto.ActionType_LIST_MESSAGES,
		EncryptedProtoData: innerBytes,
	})
	require.NoError(t, err)

	outer := &gmproto.OutgoingRPCMessage{
		Data: &gmproto.OutgoingRPCMessage_Data{RequestID: "req-1", MessageData: dataBytes},
	}
	body, err := pblite.Marshal(outer)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "https://gm/$rpc/Messaging/SendMessage", bytes.NewReader(body))
	resp, err := tr.handleSendMessage(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	require.Len(t, tr.responses, 1)
	queued := <-tr.responses
	assert.Equal(t, "emulator-response-req-1", queued.GetResponseID())
	assert.Equal(t, gmproto.BugleRoute_DataEvent, queued.GetBugleRoute())
}

// [Requirement(HARN-02)]
func TestTransport_HandleMediaDownload(t *testing.T) {
	tr := NewTransport()
	key := gmcrypto.GenerateKey(32)
	plaintext := []byte("synthetic media payload for HARN-02")
	tr.SetMedia("media-1", plaintext, key)

	downloadReq, err := proto.Marshal(&gmproto.DownloadAttachmentRequest{
		Info: &gmproto.AttachmentInfo{AttachmentID: "media-1"},
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "https://gm/$rpc/upload", nil)
	req.Header.Set("x-goog-download-metadata", base64.StdEncoding.EncodeToString(downloadReq))

	resp, err := tr.handleMediaDownload(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	encrypted, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NotEmpty(t, encrypted)

	// The emulator must emit AES-GCM ciphertext the real client can decrypt.
	cipher, err := gmcrypto.NewAESGCMHelper(key)
	require.NoError(t, err)
	decrypted, err := cipher.DecryptData(encrypted)
	require.NoError(t, err)
	assert.Equal(t, plaintext, decrypted)

	// Missing metadata is rejected.
	_, err = tr.handleMediaDownload(httptest.NewRequest(http.MethodGet, "https://gm/$rpc/upload", nil))
	require.Error(t, err)

	// Unknown media id is rejected.
	missingReq, err := proto.Marshal(&gmproto.DownloadAttachmentRequest{
		Info: &gmproto.AttachmentInfo{AttachmentID: "missing"},
	})
	require.NoError(t, err)
	badReq := httptest.NewRequest(http.MethodGet, "https://gm/$rpc/upload", nil)
	badReq.Header.Set("x-goog-download-metadata", base64.StdEncoding.EncodeToString(missingReq))
	_, err = tr.handleMediaDownload(badReq)
	require.Error(t, err)

	// Malformed base64 metadata is rejected.
	corruptReq := httptest.NewRequest(http.MethodGet, "https://gm/$rpc/upload", nil)
	corruptReq.Header.Set("x-goog-download-metadata", "!!!!not-base64!!!!")
	_, err = tr.handleMediaDownload(corruptReq)
	require.Error(t, err)
}

// [Requirement(HARN-02)]
func TestTransport_RoundTrip_HandshakeAndAck(t *testing.T) {
	tr := NewTransport()
	for _, path := range []string{"/RegisterPhoneRelay", "/web/config", "/AckMessages"} {
		resp, err := tr.RoundTrip(httptest.NewRequest(http.MethodPost, "https://gm"+path, nil))
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	}
}

// [Requirement(HARN-02)]
func TestTransport_RoundTrip_PairingThenCancellation(t *testing.T) {
	tr := NewTransport()

	// First long-poll delivers the pairing event.
	resp, err := tr.RoundTrip(httptest.NewRequest(http.MethodPost, "https://gm/$rpc/ReceiveMessages", nil))
	require.NoError(t, err)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Contains(t, string(body), "[[")

	// A cancelled long-poll returns the context error.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = tr.RoundTrip(httptest.NewRequest(http.MethodPost, "https://gm/$rpc/ReceiveMessages", nil).WithContext(ctx))
	require.Error(t, err)
}

// [Requirement(HARN-02)]
func TestTransport_RoundTrip_UnhandledEndpoint(t *testing.T) {
	tr := NewTransport()
	_, err := tr.RoundTrip(httptest.NewRequest(http.MethodPost, "https://gm/unknown/endpoint", nil))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unhandled endpoint")
}
