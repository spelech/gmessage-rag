package gm

import (
	"github.com/spelech/gmessage-rag/internal/db"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
)

// ProtoMessageToDBMessage converts a single libgm protobuf message into a
// database Message row. It returns nil for a nil input. The isBackfill flag
// distinguishes historical ETL/backfill rows from real-time inbound upserts.
func ProtoMessageToDBMessage(rawMsg *gmproto.Message, conversationID string, isBackfill bool) *db.Message {
	if rawMsg == nil {
		return nil
	}

	statusCode := int32(0)
	if rawMsg.GetMessageStatus() != nil {
		statusCode = int32(rawMsg.GetMessageStatus().GetStatus())
	}
	isFromMe, _ := ResolveSenderDirection(statusCode)
	sentAt := NormalizeTimestamp(rawMsg.GetTimestamp())

	var bodyText string
	for _, info := range rawMsg.GetMessageInfo() {
		if mc := info.GetMessageContent(); mc != nil {
			bodyText = mc.GetContent()
			break
		}
	}

	var replyToID *string
	if replyMsg := rawMsg.GetReplyMessage(); replyMsg != nil && replyMsg.GetMessageID() != "" {
		rID := replyMsg.GetMessageID()
		replyToID = &rID
	}

	var reactions []db.Reaction
	for _, rEntry := range rawMsg.GetReactions() {
		if rEntry == nil || rEntry.GetData() == nil {
			continue
		}
		emoji := rEntry.GetData().GetUnicode()
		sender := ""
		if len(rEntry.GetParticipantIDs()) > 0 {
			sender = rEntry.GetParticipantIDs()[0]
		}
		if emoji != "" {
			reactions = append(reactions, db.Reaction{
				Sender: sender,
				Emoji:  emoji,
			})
		}
	}

	senderID := rawMsg.GetParticipantID()
	if isFromMe {
		senderID = "me"
	}
	senderName := senderID
	if rawMsg.GetSenderParticipant() != nil && rawMsg.GetSenderParticipant().GetFullName() != "" {
		senderName = rawMsg.GetSenderParticipant().GetFullName()
	}

	statusVal := statusCode
	return &db.Message{
		ID:               rawMsg.GetMessageID(),
		ConversationID:   conversationID,
		SenderID:         senderID,
		SenderName:       senderName,
		IsFromMe:         isFromMe,
		SentAt:           sentAt,
		Body:             bodyText,
		ReplyToMessageID: replyToID,
		Reactions:        reactions,
		RawStatus:        &statusVal,
		IsBackfill:       isBackfill,
	}
}
