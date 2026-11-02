package emulator

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"

	gmcrypto "go.mau.fi/mautrix-gmessages/pkg/libgm/crypto"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
	"go.mau.fi/util/pblite"
	"google.golang.org/protobuf/proto"
)

// RequestCipher encrypts and decrypts libgm RPC payloads using the paired
// session keys. *crypto.AESCTRHelper satisfies it.
type RequestCipher interface {
	Encrypt(plaintext []byte) ([]byte, error)
	Decrypt(encrypted []byte) ([]byte, error)
}

// MessagePage is one cursor-addressed page of historical messages.
type MessagePage struct {
	Messages   []*gmproto.Message
	NextCursor string
}

type mediaFixture struct {
	plaintext []byte
	key       []byte
}

// SetRequestCipher installs the session cipher so the emulator can read
// encrypted RPC requests and encrypt its responses. Pass the paired client's
// AuthData.RequestCrypto.
func (t *Transport) SetRequestCipher(cipher RequestCipher) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.cipher = cipher
}

// SetMessagePages registers cursor-addressed pages for a conversation. The map
// key is the incoming cursor ("" for the first page).
func (t *Transport) SetMessagePages(conversationID string, pages map[string]MessagePage) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.messagePages[conversationID] = pages
}

// SetContacts registers the contact book returned by LIST_CONTACTS.
func (t *Transport) SetContacts(contacts []*gmproto.Contact) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.contacts = append([]*gmproto.Contact(nil), contacts...)
}

// SetMedia registers plaintext media that /upload returns encrypted under key.
func (t *Transport) SetMedia(mediaID string, plaintext, key []byte) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.media[mediaID] = mediaFixture{
		plaintext: append([]byte(nil), plaintext...),
		key:       append([]byte(nil), key...),
	}
}

// handleSendMessage decodes an outgoing RPC, synthesizes the matching typed
// response, and queues it for delivery on the long-poll stream.
func (t *Transport) handleSendMessage(req *http.Request) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, fmt.Errorf("emulator (HARN-02): read SendMessage body: %w", err)
	}

	var outgoing gmproto.OutgoingRPCMessage
	if err := pblite.Unmarshal(body, &outgoing); err != nil {
		return nil, fmt.Errorf("emulator (HARN-02): unmarshal SendMessage: %w", err)
	}

	data := outgoing.GetData()
	requestID := data.GetRequestID()

	rpcData := &gmproto.OutgoingRPCData{}
	if len(data.GetMessageData()) > 0 {
		if err := proto.Unmarshal(data.GetMessageData(), rpcData); err != nil {
			return nil, fmt.Errorf("emulator (HARN-02): unmarshal RPC data: %w", err)
		}
	}

	resp := t.buildRPCResponse(rpcData.GetAction(), rpcData.GetEncryptedProtoData())
	if resp != nil {
		if err := t.enqueueResponse(requestID, rpcData.GetAction(), resp); err != nil {
			return nil, err
		}
	}

	return protobufResponse(req, &gmproto.OutgoingRPCResponse{})
}

// buildRPCResponse maps an action to its synthetic typed response. A nil result
// means the action is fire-and-forget and needs no long-poll delivery.
func (t *Transport) buildRPCResponse(action gmproto.ActionType, encryptedReq []byte) proto.Message {
	switch action {
	case gmproto.ActionType_LIST_MESSAGES:
		var listReq gmproto.ListMessagesRequest
		t.decryptInto(encryptedReq, &listReq)

		cursorKey := ""
		if c := listReq.GetCursor(); c != nil {
			cursorKey = c.GetLastItemID()
		}

		t.mu.Lock()
		page, ok := t.messagePages[listReq.GetConversationID()][cursorKey]
		t.mu.Unlock()
		if !ok {
			return &gmproto.ListMessagesResponse{}
		}

		resp := &gmproto.ListMessagesResponse{
			Messages:      page.Messages,
			TotalMessages: int64(len(page.Messages)),
		}
		if page.NextCursor != "" {
			resp.Cursor = &gmproto.Cursor{LastItemID: page.NextCursor}
		}
		return resp

	case gmproto.ActionType_LIST_CONTACTS:
		t.mu.Lock()
		contacts := append([]*gmproto.Contact(nil), t.contacts...)
		t.mu.Unlock()
		return &gmproto.ListContactsResponse{Contacts: contacts}

	case gmproto.ActionType_IS_BUGLE_DEFAULT:
		return &gmproto.IsBugleDefaultResponse{Success: true}
	}
	return nil
}

// decryptInto best-effort decrypts an outgoing request payload into into.
// It is silent when no cipher is configured or the payload is not encrypted.
func (t *Transport) decryptInto(encrypted []byte, into proto.Message) {
	if len(encrypted) == 0 {
		return
	}
	t.mu.Lock()
	cipher := t.cipher
	t.mu.Unlock()
	if cipher == nil {
		return
	}
	plain, err := cipher.Decrypt(encrypted)
	if err != nil {
		return
	}
	_ = proto.Unmarshal(plain, into)
}

// enqueueResponse encrypts and frames a typed response for long-poll delivery.
func (t *Transport) enqueueResponse(requestID string, action gmproto.ActionType, response proto.Message) error {
	payload, err := proto.Marshal(response)
	if err != nil {
		return fmt.Errorf("emulator (HARN-02): marshal %T: %w", response, err)
	}

	t.mu.Lock()
	cipher := t.cipher
	t.mu.Unlock()
	if cipher == nil {
		return fmt.Errorf("emulator (HARN-02): no request cipher configured")
	}
	encrypted, err := cipher.Encrypt(payload)
	if err != nil {
		return fmt.Errorf("emulator (HARN-02): encrypt %T: %w", response, err)
	}

	messageData, err := proto.Marshal(&gmproto.RPCMessageData{
		SessionID:     requestID,
		Action:        action,
		EncryptedData: encrypted,
	})
	if err != nil {
		return fmt.Errorf("emulator (HARN-02): marshal RPC message data: %w", err)
	}

	incoming := &gmproto.IncomingRPCMessage{
		ResponseID:  "emulator-response-" + requestID,
		BugleRoute:  gmproto.BugleRoute_DataEvent,
		MessageData: messageData,
	}

	select {
	case t.responses <- incoming:
	default:
	}
	return nil
}

// responseFrame wraps a queued response in the [[ ... ]] long-poll framing used
// by readLongPoll. Each segment is returned by a distinct Read call.
func (t *Transport) responseFrame(req *http.Request, incoming *gmproto.IncomingRPCMessage) (*http.Response, error) {
	element, err := pblite.Marshal(&gmproto.LongPollingPayload{Data: incoming})
	if err != nil {
		return nil, fmt.Errorf("emulator (HARN-02): marshal long-poll response: %w", err)
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

// handleMediaDownload serves encrypted media bytes for a registered fixture.
func (t *Transport) handleMediaDownload(req *http.Request) (*http.Response, error) {
	metadata := req.Header.Get("x-goog-download-metadata")
	if metadata == "" {
		return nil, fmt.Errorf("emulator (HARN-02): missing x-goog-download-metadata")
	}

	raw, err := base64.StdEncoding.DecodeString(metadata)
	if err != nil {
		return nil, fmt.Errorf("emulator (HARN-02): decode download metadata: %w", err)
	}

	var downloadReq gmproto.DownloadAttachmentRequest
	if err := proto.Unmarshal(raw, &downloadReq); err != nil {
		return nil, fmt.Errorf("emulator (HARN-02): unmarshal download request: %w", err)
	}
	mediaID := downloadReq.GetInfo().GetAttachmentID()

	t.mu.Lock()
	fixture, ok := t.media[mediaID]
	t.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("emulator (HARN-02): no media fixture %q", mediaID)
	}

	encrypted, err := encryptMediaFixture(fixture)
	if err != nil {
		return nil, err
	}

	return &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type": []string{"application/octet-stream"},
		},
		Body:    io.NopCloser(bytes.NewReader(encrypted)),
		Request: req,
	}, nil
}

// encryptMediaFixture encrypts plaintext with the AES-GCM media format libgm decodes.
func encryptMediaFixture(f mediaFixture) ([]byte, error) {
	cipher, err := gmcrypto.NewAESGCMHelper(f.key)
	if err != nil {
		return nil, fmt.Errorf("emulator (HARN-02): media cipher: %w", err)
	}
	encrypted, err := cipher.EncryptData(f.plaintext)
	if err != nil {
		return nil, fmt.Errorf("emulator (HARN-02): encrypt media: %w", err)
	}
	return encrypted, nil
}
