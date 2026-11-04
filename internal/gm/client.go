package gm

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
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

// TypingActivityFunc is called when a participant typing indicator arrives for a conversation (PIPE-05).
type TypingActivityFunc func(conversationID string, typingType gmproto.TypingTypes)

// PhoneStatusFunc is called when phone responsiveness state changes (TRANS-04).
type PhoneStatusFunc func()

// FatalErrorFunc is called when libgm encounters an unrecoverable listener error (AUTH-03).
type FatalErrorFunc func(err error)

// ClientOptions configures the GM client wrapper.
type ClientOptions struct {
	SessionPath            string
	Logger                 zerolog.Logger
	OnMessageUpsert        MessageUpsertFunc
	OnConversationActive   ConversationActivityFunc
	OnTypingActivity       TypingActivityFunc
	OnPhoneNotResponding   PhoneStatusFunc
	OnPhoneRespondingAgain PhoneStatusFunc
	OnListenFatalError     FatalErrorFunc
	HTTPClientSettings     *exhttp.ClientSettings
}

// Client wraps libgm.Client with session lifecycle, normalization, and event dispatch.
type Client struct {
	libgmClient            *libgm.Client
	authData               *libgm.AuthData
	sessionPath            string
	logger                 zerolog.Logger
	onMessageUpsert        MessageUpsertFunc
	onConversationActive   ConversationActivityFunc
	onTypingActivity       TypingActivityFunc
	onPhoneNotResponding   PhoneStatusFunc
	onPhoneRespondingAgain PhoneStatusFunc
	onListenFatalError     FatalErrorFunc

	mu            sync.RWMutex
	pairDone      chan struct{}
	lastPairError error
	currentEmoji  string
	connecting    bool
}

// Compile-time conformance for the ingestion seams the daemon and ETL consume.
var (
	_ MessageFetcher  = (*Client)(nil)
	_ ContactsFetcher = (*Client)(nil)
	_ MediaDownloader = (*Client)(nil)
)

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
		libgmClient:            libgmCli,
		authData:               authData,
		sessionPath:            sessionPath,
		logger:                 opts.Logger,
		onMessageUpsert:        opts.OnMessageUpsert,
		onConversationActive:   opts.OnConversationActive,
		onTypingActivity:       opts.OnTypingActivity,
		onPhoneNotResponding:   opts.OnPhoneNotResponding,
		onPhoneRespondingAgain: opts.OnPhoneRespondingAgain,
		onListenFatalError:     opts.OnListenFatalError,
		pairDone:               make(chan struct{}),
	}

	libgmCli.SetEventHandler(c.handleEvent)

	return c, nil
}

// FetchMessages wraps libgm.Client.FetchMessages converting byte cursor to *gmproto.Cursor.
// This implements MessageFetcher for ETL and backfill.
func (c *Client) FetchMessages(ctx context.Context, conversationID string, count int64, cursor []byte) (*gmproto.ListMessagesResponse, error) {
	var protoCursor *gmproto.Cursor
	if len(cursor) > 0 {
		s := string(cursor)
		if parts := strings.SplitN(s, ":", 2); len(parts) == 2 {
			var ts int64
			_, _ = fmt.Sscanf(parts[1], "%d", &ts)
			protoCursor = &gmproto.Cursor{
				LastItemID:        parts[0],
				LastItemTimestamp: ts,
			}
		} else {
			protoCursor = &gmproto.Cursor{
				LastItemID: s,
			}
		}
	}
	resp, err := c.libgmClient.FetchMessages(ctx, conversationID, count, protoCursor)
	_ = SaveAuthData(c.sessionPath, c.authData)
	return resp, err
}

// ListConversations wraps libgm.Client.ListConversations to fetch remote conversations.
func (c *Client) ListConversations(ctx context.Context, count int64) (*gmproto.ListConversationsResponse, error) {
	req := &gmproto.ListConversationsRequest{
		Count:  count,
		Folder: gmproto.ListConversationsRequest_INBOX,
	}
	resp, err := c.libgmClient.ListConversations(ctx, req)
	_ = SaveAuthData(c.sessionPath, c.authData)
	return resp, err
}

// ListContacts wraps libgm.Client.ListContacts, exposing the Google Contacts roster for
// dossier enrichment (PIPE-04).
func (c *Client) ListContacts(ctx context.Context) (*gmproto.ListContactsResponse, error) {
	resp, err := c.libgmClient.ListContacts(ctx)
	_ = SaveAuthData(c.sessionPath, c.authData)
	return resp, err
}

// ListTopContacts wraps libgm.Client.ListTopContacts, exposing the frequently contacted roster
// for dossier enrichment (PIPE-04).
func (c *Client) ListTopContacts(ctx context.Context) (*gmproto.ListTopContactsResponse, error) {
	resp, err := c.libgmClient.ListTopContacts(ctx)
	_ = SaveAuthData(c.sessionPath, c.authData)
	return resp, err
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

	case *gmproto.TypingData:
		c.handleTypingData(e)

	case *events.PhoneNotResponding:
		c.logger.Warn().Msg("Phone is not responding")
		if c.onPhoneNotResponding != nil {
			c.onPhoneNotResponding()
		}

	case *events.PhoneRespondingAgain:
		c.logger.Info().Msg("Phone is responding again")
		if c.onPhoneRespondingAgain != nil {
			c.onPhoneRespondingAgain()
		}

	case *events.ListenFatalError:
		c.logger.Error().Err(e.Error).Msg("Fatal listener error in libgm")
		c.mu.Lock()
		c.lastPairError = e.Error
		c.connecting = false
		c.mu.Unlock()
		if c.onListenFatalError != nil {
			c.onListenFatalError(e.Error)
		}

	default:
		c.logger.Trace().Type("event_type", evt).Msg("Received other libgm event")
	}
}

func (c *Client) handleIncomingMessage(msg *gmproto.Message) {
	if msg == nil {
		return
	}
	_ = SaveAuthData(c.sessionPath, c.authData)

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

// handleTypingData forwards a participant typing indicator to the action-aware
// debouncer so an active conversation holds its idle window open (PIPE-05).
func (c *Client) handleTypingData(data *gmproto.TypingData) {
	if data == nil {
		return
	}

	convID := data.GetConversationID()
	if convID == "" || c.onTypingActivity == nil {
		return
	}

	c.onTypingActivity(convID, data.GetType())
}

// Connect starts connection recovery or background session.
// It is idempotent if the client is already connected or actively polling.
func (c *Client) Connect(ctx context.Context) error {
	if !c.libgmClient.IsLoggedIn() {
		return errors.New("cannot connect: client is not logged in / paired")
	}
	c.mu.Lock()
	if c.connecting {
		c.mu.Unlock()
		return nil
	}
	c.connecting = true
	c.mu.Unlock()

	err := c.libgmClient.Connect(ctx)
	if err != nil {
		c.mu.Lock()
		c.connecting = false
		c.mu.Unlock()
		return err
	}
	return nil
}

// ReconnectLoop maintains connection health by periodically checking connection status
// rather than tight-spinning on Connect().
func (c *Client) ReconnectLoop(ctx context.Context) {
	backoff := 5 * time.Second
	const maxBackoff = 60 * time.Second
	const checkInterval = 15 * time.Second

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

		// If libgm is already connected, wait for checkInterval before checking again
		if c.libgmClient.IsConnected() {
			select {
			case <-ctx.Done():
				return
			case <-time.After(checkInterval):
				continue
			}
		}

		c.logger.Info().Msg("Google Messages client not connected, establishing connection...")
		err := c.Connect(ctx)
		if err != nil {
			c.logger.Warn().Err(err).Dur("retry_in", backoff).Msg("Connect attempt failed, backing off")
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff = time.Duration(math.Min(float64(backoff*2), float64(maxBackoff)))
		} else {
			backoff = 5 * time.Second
			// Wait for connection to settle
			select {
			case <-ctx.Done():
				return
			case <-time.After(checkInterval):
			}
		}
	}
}

// Disconnect tears down the long-poll connection without clearing the session
// cookies or the pairing. A later Connect resumes the same session, which the
// periodic sync loop relies on.
func (c *Client) Disconnect() {
	if c == nil || c.libgmClient == nil {
		return
	}
	c.libgmClient.Disconnect()
	c.mu.Lock()
	c.connecting = false
	c.mu.Unlock()
}

// IsConnected reports whether the libgm long-poll connection is currently open.
func (c *Client) IsConnected() bool {
	if c == nil || c.libgmClient == nil {
		return false
	}
	return c.libgmClient.IsConnected()
}

// IsLoggedIn reports whether a paired session with cookies is loaded.
func (c *Client) IsLoggedIn() bool {
	if c == nil || c.libgmClient == nil {
		return false
	}
	return c.libgmClient.IsLoggedIn()
}

// SetPingInterval overrides the ditto ping interval. libgm accepts 1m..4h.
func (c *Client) SetPingInterval(d time.Duration) {
	if c == nil || c.libgmClient == nil {
		return
	}
	c.libgmClient.SetPingInterval(d)
}

// SetAlertTimeoutCount overrides how many missed pings mark the phone unresponsive.
func (c *Client) SetAlertTimeoutCount(n int) {
	if c == nil || c.libgmClient == nil {
		return
	}
	c.libgmClient.SetAlertTimeoutCount(n)
}
