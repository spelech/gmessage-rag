package pipeline

import (
	"context"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
)

// DefaultIdleTimeout is the standard idle threshold before flushing a conversation session.
const DefaultIdleTimeout = 30 * time.Minute

// DebounceEventType represents the kind of conversation activity observed.
type DebounceEventType string

const (
	// EventTypingStarted represents a participant starting to type.
	EventTypingStarted DebounceEventType = "typing_started"
	// EventTypingStopped represents a participant stopping typing.
	EventTypingStopped DebounceEventType = "typing_stopped"
	// EventOutboundMessage represents a newly drafted or sent outbound message.
	EventOutboundMessage DebounceEventType = "outbound_message"
	// EventInboundMessage represents an incoming message from a contact.
	EventInboundMessage DebounceEventType = "inbound_message"
)

// FlushCallback is called when a conversation's idle timer elapses without new activity.
type FlushCallback func(ctx context.Context, conversationID string) error

// DebouncerConfig configures the SmartDebouncer.
type DebouncerConfig struct {
	// IdleTimeout specifies the period of silence before OnFlush is invoked (default: 30 minutes).
	IdleTimeout time.Duration
	// OnFlush is executed when a conversation reaches idle timeout.
	OnFlush FlushCallback
	// Logger is the optional structured logger.
	Logger zerolog.Logger
}

type conversationTimer struct {
	timer      *time.Timer
	convID     string
	generation uint64
	lastActive time.Time
}

// SmartDebouncer manages action-aware per-conversation idle countdown timers (PIPE-05).
type SmartDebouncer struct {
	mu          sync.Mutex
	idleTimeout time.Duration
	onFlush     FlushCallback
	logger      zerolog.Logger
	timers      map[string]*conversationTimer
	closed      bool
	wg          sync.WaitGroup
	ctx         context.Context
	cancel      context.CancelFunc
}

// NewSmartDebouncer instantiates a new SmartDebouncer.
func NewSmartDebouncer(cfg DebouncerConfig) *SmartDebouncer {
	timeout := cfg.IdleTimeout
	if timeout <= 0 {
		timeout = DefaultIdleTimeout
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &SmartDebouncer{
		idleTimeout: timeout,
		onFlush:     cfg.OnFlush,
		logger:      cfg.Logger,
		timers:      make(map[string]*conversationTimer),
		ctx:         ctx,
		cancel:      cancel,
	}
}

// RecordEvent records an action-aware event for a conversation.
// Resets or extends the idle timer on typing indicator or message events.
func (d *SmartDebouncer) RecordEvent(convID string, eventType DebounceEventType) {
	if convID == "" {
		return
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	if d.closed {
		return
	}

	switch eventType {
	case EventTypingStarted, EventOutboundMessage, EventInboundMessage:
		d.resetTimerLocked(convID)
	case EventTypingStopped:
		// if timer exists, typing_stopped does not reset it
	default:
		d.resetTimerLocked(convID)
	}
}

// RecordTypingType is a convenience method mapping gmproto.TypingTypes to debounce events.
func (d *SmartDebouncer) RecordTypingType(convID string, typingType gmproto.TypingTypes) {
	if typingType == gmproto.TypingTypes_STARTED_TYPING {
		d.RecordEvent(convID, EventTypingStarted)
	} else if typingType == gmproto.TypingTypes_STOPPED_TYPING {
		d.RecordEvent(convID, EventTypingStopped)
	}
}

// resetTimerLocked stops any existing timer and schedules a new flush after idleTimeout.
func (d *SmartDebouncer) resetTimerLocked(convID string) {
	if existing, found := d.timers[convID]; found {
		existing.timer.Stop()
		existing.generation++
		existing.lastActive = time.Now()
		gen := existing.generation
		existing.timer = time.AfterFunc(d.idleTimeout, func() {
			d.onTimerFired(convID, gen)
		})
		return
	}

	ct := &conversationTimer{
		convID:     convID,
		generation: 1,
		lastActive: time.Now(),
	}

	gen := ct.generation
	ct.timer = time.AfterFunc(d.idleTimeout, func() {
		d.onTimerFired(convID, gen)
	})

	d.timers[convID] = ct
}

func (d *SmartDebouncer) onTimerFired(convID string, gen uint64) {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return
	}

	existing, found := d.timers[convID]
	if !found || existing.generation != gen {
		// Outdated generation or already removed
		d.mu.Unlock()
		return
	}

	delete(d.timers, convID)

	flushFn := d.onFlush
	debouncerCtx := d.ctx
	d.wg.Add(1)
	d.mu.Unlock()

	go func() {
		defer d.wg.Done()
		if flushFn != nil {
			if err := flushFn(debouncerCtx, convID); err != nil && debouncerCtx.Err() == nil {
				d.logger.Error().Err(err).Str("conv_id", convID).Msg("Flush callback failed")
			}
		}
	}()
}

// ActiveCount returns the number of active conversation timers.
func (d *SmartDebouncer) ActiveCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.timers)
}

// Close cancels all pending conversation timers and awaits outstanding flush callbacks without leaking goroutines.
func (d *SmartDebouncer) Close() error {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return nil
	}
	d.closed = true
	d.cancel()

	for convID, ct := range d.timers {
		ct.timer.Stop()
		delete(d.timers, convID)
	}
	d.mu.Unlock()

	d.wg.Wait()
	return nil
}
