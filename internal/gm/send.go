package gm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/util"
)

// OutgoingMessage describes a text message to send into an existing conversation.
// OutgoingID and SIMPayload are optional. When empty, Google Messages routes the
// send with the account's default outgoing identity. Callers that captured
// conversation metadata (default_outgoing_id / sim_payload) should pass them so
// multi-SIM and RCS routing stays correct.
type OutgoingMessage struct {
	ConversationID string
	Text           string
	ReplyToID      string
	OutgoingID     string
	SIMPayload     *gmproto.SIMPayload
	ForceRCS       bool
}

// OutgoingMedia describes a media message to send into an existing conversation.
type OutgoingMedia struct {
	ConversationID string
	FileName       string
	MimeType       string
	Data           []byte
	Caption        string
	ReplyToID      string
	OutgoingID     string
	SIMPayload     *gmproto.SIMPayload
	ForceRCS       bool
}

// SendResult is the outcome of an accepted outbound send. Google Messages does
// not return the assigned message ID in the send RPC response; the echoed event
// carries the final ID and TmpID. TmpID lets a caller correlate the optimistic
// send with that echo.
type SendResult struct {
	TmpID          string    `json:"tmp_id"`
	ConversationID string    `json:"conversation_id"`
	SentAt         time.Time `json:"sent_at"`
	Status         string    `json:"status"`
	MediaID        string    `json:"media_id,omitempty"`
}

// SetSendSettle overrides how long connect-on-send holds an otherwise-unused
// connection open after the RPC so the outgoing echo can be persisted.
func (c *Client) SetSendSettle(d time.Duration) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.sendSettle = d
	c.mu.Unlock()
}

func (c *Client) getSendSettle() time.Duration {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.sendSettle
}

// withConnection runs fn while a long-poll connection is available. If the
// client is already connected it reuses that connection. Otherwise it opens a
// connection, runs fn, and holds briefly so the outgoing echo can arrive. The
// connection is left open (strategy 1a): the periodic sync loop owns teardown
// at the end of its window, and intermediate sends reuse the same connection.
func (c *Client) withConnection(ctx context.Context, fn func(context.Context) error) error {
	if c == nil || c.libgmClient == nil {
		return errors.New("gm client is not initialized")
	}
	if !c.libgmClient.IsLoggedIn() {
		return errors.New("cannot send: client is not logged in / paired")
	}

	if c.IsConnected() {
		return fn(ctx)
	}

	connectCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := c.Connect(connectCtx); err != nil {
		return fmt.Errorf("connect for send: %w", err)
	}

	if err := fn(ctx); err != nil {
		return err
	}

	// Hold the connection briefly so the outgoing echo arrives and the event
	// pipeline persists the sent message and its media.
	if settle := c.getSendSettle(); settle > 0 {
		select {
		case <-ctx.Done():
		case <-time.After(settle):
		}
	}
	return nil
}

// SendText sends a text message into an existing conversation. It validates the
// request, opens a connection when needed, and returns once Google Messages
// accepts the send. Outgoing persistence happens through the event pipeline
// when the echoed message arrives during the settle hold.
func (c *Client) SendText(ctx context.Context, msg OutgoingMessage) (*SendResult, error) {
	if c == nil {
		return nil, errors.New("gm client is not initialized")
	}
	conversationID := strings.TrimSpace(msg.ConversationID)
	if conversationID == "" {
		return nil, errors.New("conversation id is required")
	}
	text := strings.TrimSpace(msg.Text)
	if text == "" {
		return nil, errors.New("text is required")
	}

	tmpID := util.GenerateTmpID()
	req := &gmproto.SendMessageRequest{
		ConversationID: conversationID,
		TmpID:          tmpID,
		MessagePayload: &gmproto.MessagePayload{
			TmpID:          tmpID,
			TmpID2:         tmpID,
			ConversationID: conversationID,
			ParticipantID:  strings.TrimSpace(msg.OutgoingID),
			MessageInfo: []*gmproto.MessageInfo{{
				Data: &gmproto.MessageInfo_MessageContent{MessageContent: &gmproto.MessageContent{
					Content: text,
				}},
			}},
		},
		SIMPayload: msg.SIMPayload,
		ForceRCS:   msg.ForceRCS,
	}
	if replyTo := strings.TrimSpace(msg.ReplyToID); replyTo != "" {
		req.Reply = &gmproto.ReplyPayload{MessageID: replyTo}
	}

	var resp *gmproto.SendMessageResponse
	err := c.withConnection(ctx, func(cctx context.Context) error {
		r, e := c.libgmClient.SendMessage(cctx, req)
		resp = r
		return e
	})
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, errors.New("send message returned no response")
	}
	if resp.GetStatus() != gmproto.SendMessageResponse_SUCCESS {
		return nil, fmt.Errorf("send message rejected: status %s", resp.GetStatus().String())
	}

	return &SendResult{
		TmpID:          tmpID,
		ConversationID: conversationID,
		SentAt:         time.Now().UTC(),
		Status:         resp.GetStatus().String(),
	}, nil
}

// SendMedia uploads and sends a media message. It reuses the same connect-on-send
// path as SendText. The returned MediaID identifies the uploaded blob so a caller
// can pre-record the attachment before the echo arrives.
func (c *Client) SendMedia(ctx context.Context, msg OutgoingMedia) (*SendResult, error) {
	if c == nil {
		return nil, errors.New("gm client is not initialized")
	}
	conversationID := strings.TrimSpace(msg.ConversationID)
	if conversationID == "" {
		return nil, errors.New("conversation id is required")
	}
	if len(msg.Data) == 0 {
		return nil, errors.New("media data is required")
	}
	fileName := strings.TrimSpace(msg.FileName)
	if fileName == "" {
		fileName = "attachment"
	}
	mimeType := strings.TrimSpace(msg.MimeType)
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	var (
		tmpID     = util.GenerateTmpID()
		mediaID   string
		statusStr string
	)
	err := c.withConnection(ctx, func(cctx context.Context) error {
		media, err := c.libgmClient.UploadMedia(msg.Data, fileName, mimeType)
		if err != nil {
			return fmt.Errorf("upload media: %w", err)
		}
		mediaID = media.GetMediaID()

		info := []*gmproto.MessageInfo{{
			Data: &gmproto.MessageInfo_MediaContent{MediaContent: media},
		}}
		if caption := strings.TrimSpace(msg.Caption); caption != "" {
			info = append(info, &gmproto.MessageInfo{
				Data: &gmproto.MessageInfo_MessageContent{MessageContent: &gmproto.MessageContent{
					Content: caption,
				}},
			})
		}

		req := &gmproto.SendMessageRequest{
			ConversationID: conversationID,
			TmpID:          tmpID,
			MessagePayload: &gmproto.MessagePayload{
				TmpID:          tmpID,
				TmpID2:         tmpID,
				ConversationID: conversationID,
				ParticipantID:  strings.TrimSpace(msg.OutgoingID),
				MessageInfo:    info,
			},
			SIMPayload: msg.SIMPayload,
			ForceRCS:   msg.ForceRCS,
		}
		if replyTo := strings.TrimSpace(msg.ReplyToID); replyTo != "" {
			req.Reply = &gmproto.ReplyPayload{MessageID: replyTo}
		}

		resp, err := c.libgmClient.SendMessage(cctx, req)
		if err != nil {
			return err
		}
		if resp == nil {
			return errors.New("send media returned no response")
		}
		if resp.GetStatus() != gmproto.SendMessageResponse_SUCCESS {
			return fmt.Errorf("send media rejected: status %s", resp.GetStatus().String())
		}
		statusStr = resp.GetStatus().String()
		return nil
	})
	if err != nil {
		return nil, err
	}

	return &SendResult{
		TmpID:          tmpID,
		ConversationID: conversationID,
		SentAt:         time.Now().UTC(),
		Status:         statusStr,
		MediaID:        mediaID,
	}, nil
}

// SendReaction adds or removes an emoji reaction on an existing message.
// A removal uses SendReactionRequest_REMOVE and requires no emoji match check.
func (c *Client) SendReaction(ctx context.Context, messageID, emoji string, remove bool, simPayload *gmproto.SIMPayload) error {
	if c == nil {
		return errors.New("gm client is not initialized")
	}
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return errors.New("message id is required")
	}
	emoji = strings.TrimSpace(emoji)
	if emoji == "" && !remove {
		return errors.New("emoji is required to add a reaction")
	}

	action := gmproto.SendReactionRequest_ADD
	if remove {
		action = gmproto.SendReactionRequest_REMOVE
	}

	return c.withConnection(ctx, func(cctx context.Context) error {
		resp, err := c.libgmClient.SendReaction(cctx, &gmproto.SendReactionRequest{
			MessageID:    messageID,
			ReactionData: gmproto.MakeReactionData(emoji),
			Action:       action,
			SIMPayload:   simPayload,
		})
		if err != nil {
			return err
		}
		if resp == nil || !resp.GetSuccess() {
			return errors.New("reaction send was rejected")
		}
		return nil
	})
}
