package gm

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"go.mau.fi/mautrix-gmessages/pkg/libgm"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/events"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
	exhttp "go.mau.fi/util/exhttp"
)

// DefaultSessionPath is the default path to persist session credentials.
const DefaultSessionPath = "/data/auth/session.json"

// newLibGMClient is the constructor seam for the underlying libgm client.
// Tests replace it to inject hermetic HTTP client settings (HARN-03) without forking libgm.
var newLibGMClient = libgm.NewClient

// MessageUpsertFunc is called whenever a message is received or processed.
type MessageUpsertFunc func(ctx context.Context, msg *gmproto.Message, isFromMe bool, sentAt time.Time) error

// ConversationActivityFunc is called to reset or notify conversation activity (e.g. idle debouncer).
type ConversationActivityFunc func(conversationID string)

// ClientOptions configures the GM client wrapper.
type ClientOptions struct {
	SessionPath          string
	Logger               zerolog.Logger
	OnMessageUpsert      MessageUpsertFunc
	OnConversationActive ConversationActivityFunc
	HTTPClientSettings   *exhttp.ClientSettings
}

// Client wraps libgm.Client with session lifecycle, normalization, and event dispatch.
type Client struct {
	libgmClient          *libgm.Client
	authData             *libgm.AuthData
	sessionPath          string
	logger               zerolog.Logger
	onMessageUpsert      MessageUpsertFunc
	onConversationActive ConversationActivityFunc

	mu            sync.RWMutex
	pairDone      chan struct{}
	lastPairError error
	currentQR     string
	currentEmoji  string
}

// NewClient initializes a new GM Client wrapper.
func NewClient(opts ClientOptions) (*Client, error) {
	sessionPath := opts.SessionPath
	if sessionPath == "" {
		sessionPath = DefaultSessionPath
	}

	authData, err := LoadAuthData(sessionPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load auth data: %w", err)
	}

	httpSettings := exhttp.ClientSettings{}
	if opts.HTTPClientSettings != nil {
		httpSettings = *opts.HTTPClientSettings
	}

	libgmCli := newLibGMClient(authData, nil, opts.Logger, httpSettings)

	c := &Client{
		libgmClient:          libgmCli,
		authData:             authData,
		sessionPath:          sessionPath,
		logger:               opts.Logger,
		onMessageUpsert:      opts.OnMessageUpsert,
		onConversationActive: opts.OnConversationActive,
		pairDone:             make(chan struct{}),
	}

	libgmCli.SetEventHandler(c.handleEvent)

	return c, nil
}

// FetchMessages wraps libgm.Client.FetchMessages converting byte cursor to *gmproto.Cursor.
// This implements MessageFetcher for ETL and backfill.
func (c *Client) FetchMessages(ctx context.Context, conversationID string, count int64, cursor []byte) (*gmproto.ListMessagesResponse, error) {
	var protoCursor *gmproto.Cursor
	if len(cursor) > 0 {
		protoCursor = &gmproto.Cursor{
			LastItemID: string(cursor),
		}
	}
	return c.libgmClient.FetchMessages(ctx, conversationID, count, protoCursor)
}

// LibGM returns the underlying libgm.Client.
func (c *Client) LibGM() *libgm.Client {
	return c.libgmClient
}

// AuthData returns current AuthData.
func (c *Client) AuthData() *libgm.AuthData {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.authData
}

// SessionPath returns the configured session path.
func (c *Client) SessionPath() string {
	return c.sessionPath
}

// NormalizeTimestamp inspects timestamp magnitude and normalizes to UTC time.Time.
// Microseconds: > 10^14 (e.g., 1700000000000000)
// Milliseconds: > 10^11 (e.g., 1700000000000)
// Seconds: <= 10^11
func NormalizeTimestamp(ts int64) time.Time {
	if ts <= 0 {
		return time.Now().UTC()
	}

	const microThreshold = 100_000_000_000_000 // 10^14
	const milliThreshold = 100_000_000_000     // 10^11

	switch {
	case ts > microThreshold:
		sec := ts / 1_000_000
		usec := ts % 1_000_000
		return time.Unix(sec, usec*1_000).UTC()
	case ts > milliThreshold:
		sec := ts / 1_000
		msec := ts % 1_000
		return time.Unix(sec, msec*1_000_000).UTC()
	default:
		return time.Unix(ts, 0).UTC()
	}
}

// ResolveSenderDirection determines if message is outgoing or incoming based on Bugle status code.
// Status 0 <= status < 100 (OUTGOING_*) -> is_from_me = true
// Status 100 <= status < 200 (INCOMING_*) -> is_from_me = false
// If status is outside these ranges or unknown, returns false and false for valid.
func ResolveSenderDirection(statusCode int32) (isFromMe bool, ok bool) {
	if statusCode >= 0 && statusCode < 100 {
		return true, true
	}
	if statusCode >= 100 && statusCode < 200 {
		return false, true
	}
	return false, false
}

// IsFromMe resolves direction with a fallback when status code is outside the 0-199 range.
func IsFromMe(status gmproto.MessageStatusType) bool {
	code := int32(status)
	if isFromMe, ok := ResolveSenderDirection(code); ok {
		return isFromMe
	}
	return false
}

// handleEvent processes libgm events and routes to persistence, debouncer, and DB upserts.
func (c *Client) handleEvent(evt any) {
	switch e := evt.(type) {
	case *events.PairSuccessful:
		c.logger.Info().Str("phone_id", e.PhoneID).Msg("Pairing successful")
		if err := SaveAuthData(c.sessionPath, c.authData); err != nil {
			c.logger.Error().Err(err).Msg("Failed to save auth data on PairSuccessful")
		}
		c.mu.Lock()
		select {
		case <-c.pairDone:
		default:
			close(c.pairDone)
		}
		c.mu.Unlock()

	case *events.AuthTokenRefreshed:
		c.logger.Info().Msg("Auth token refreshed, saving session")
		if err := SaveAuthData(c.sessionPath, c.authData); err != nil {
			c.logger.Error().Err(err).Msg("Failed to save auth data on AuthTokenRefreshed")
		}

	case *libgm.WrappedMessage:
		c.handleIncomingMessage(e.Message)

	case *gmproto.Message:
		c.handleIncomingMessage(e)

	case *events.ListenFatalError:
		c.logger.Error().Err(e.Error).Msg("Fatal listener error in libgm")
		c.mu.Lock()
		c.lastPairError = e.Error
		c.mu.Unlock()

	default:
		c.logger.Trace().Type("event_type", evt).Msg("Received other libgm event")
	}
}

func (c *Client) handleIncomingMessage(msg *gmproto.Message) {
	if msg == nil {
		return
	}

	convID := msg.GetConversationID()
	if convID != "" && c.onConversationActive != nil {
		c.onConversationActive(convID)
	}

	statusCode := int32(0)
	if msg.GetMessageStatus() != nil {
		statusCode = int32(msg.GetMessageStatus().GetStatus())
	}
	isFromMe, _ := ResolveSenderDirection(statusCode)
	sentAt := NormalizeTimestamp(msg.GetTimestamp())

	if c.onMessageUpsert != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := c.onMessageUpsert(ctx, msg, isFromMe, sentAt); err != nil {
			c.logger.Error().Err(err).Str("message_id", msg.GetMessageID()).Msg("Failed to upsert message")
		}
	}
}

// Connect starts connection recovery or background session.
func (c *Client) Connect(ctx context.Context) error {
	if !c.libgmClient.IsLoggedIn() {
		return errors.New("cannot connect: client is not logged in / paired")
	}
	return c.libgmClient.Connect(ctx)
}

// ReconnectLoop handles reconnection with exponential backoff.
func (c *Client) ReconnectLoop(ctx context.Context) {
	backoff := 1 * time.Second
	const maxBackoff = 60 * time.Second

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if !c.libgmClient.IsLoggedIn() {
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
				continue
			}
		}

		err := c.libgmClient.Connect(ctx)
		if err != nil {
			c.logger.Warn().Err(err).Dur("retry_in", backoff).Msg("Connection lost, retrying")
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff = time.Duration(math.Min(float64(backoff*2), float64(maxBackoff)))
		} else {
			backoff = 1 * time.Second
		}
	}
}
