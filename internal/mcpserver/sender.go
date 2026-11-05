package mcpserver

import (
	"context"

	"github.com/spelech/gmessage-rag/internal/gm"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
)

// MessageSender is the outbound transport surface the MCP server needs.
// *gm.Client implements it. Keeping it an interface lets the server stay
// decoupled from a live Google connection and easy to test.
type MessageSender interface {
	SendText(ctx context.Context, msg gm.OutgoingMessage) (*gm.SendResult, error)
	SendMedia(ctx context.Context, msg gm.OutgoingMedia) (*gm.SendResult, error)
	SendReaction(ctx context.Context, messageID, emoji string, remove bool, simPayload *gmproto.SIMPayload) error
}
