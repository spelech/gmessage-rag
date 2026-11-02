package pipeline

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
)

// TestDebouncer_ResetOnNewMessage tests basic timer expiration and message-based reset (PIPE-02).
func TestDebouncer_ResetOnNewMessage(t *testing.T) {
	flushed := make(chan string, 5)
	debouncer := NewSmartDebouncer(DebouncerConfig{
		IdleTimeout: 100 * time.Millisecond,
		OnFlush: func(ctx context.Context, conversationID string) error {
			flushed <- conversationID
			return nil
		},
	})
	defer func() {
		require.NoError(t, debouncer.Close())
	}()

	// Record initial message event
	debouncer.RecordEvent("conv-1", EventInboundMessage)
	assert.Equal(t, 1, debouncer.ActiveCount())

	// Advance 40ms, send outbound message to reset timer
	time.Sleep(40 * time.Millisecond)
	debouncer.RecordEvent("conv-1", EventOutboundMessage)

	// Wait another 40ms; total 80ms since start, but only 40ms since last event.
	// Timer shouldn't have fired yet.
	time.Sleep(40 * time.Millisecond)
	select {
	case conv := <-flushed:
		t.Fatalf("Timer fired prematurely for %s", conv)
	default:
	}

	// Now wait for full expiration (>100ms since reset)
	select {
	case conv := <-flushed:
		assert.Equal(t, "conv-1", conv)
	case <-time.After(300 * time.Millisecond):
		t.Fatal("Expected flush callback to execute")
	}

	assert.Equal(t, 0, debouncer.ActiveCount())
}

// TestDebouncer_TypingIndicatorReset verifies participant typing indicators extend/reset the timer (PIPE-05).
func TestDebouncer_TypingIndicatorReset(t *testing.T) {
	flushed := make(chan string, 5)
	debouncer := NewSmartDebouncer(DebouncerConfig{
		IdleTimeout: 100 * time.Millisecond,
		OnFlush: func(ctx context.Context, conversationID string) error {
			flushed <- conversationID
			return nil
		},
	})
	defer func() {
		require.NoError(t, debouncer.Close())
	}()

	debouncer.RecordEvent("conv-typing", EventInboundMessage)

	// Repeatedly send typing started indicators to hold window open
	for i := 0; i < 3; i++ {
		time.Sleep(40 * time.Millisecond)
		debouncer.RecordTypingType("conv-typing", gmproto.TypingTypes_STARTED_TYPING)
	}

	// Total elapsed is 120ms, but each typing event extended by 100ms.
	// It should NOT have flushed yet.
	select {
	case conv := <-flushed:
		t.Fatalf("Timer fired unexpectedly during active typing: %s", conv)
	default:
	}

	// Wait for silence
	select {
	case conv := <-flushed:
		assert.Equal(t, "conv-typing", conv)
	case <-time.After(300 * time.Millisecond):
		t.Fatal("Expected flush callback to fire after typing stopped and timeout elapsed")
	}
}

// [Requirement(PIPE-05)]
// TestDebouncer_TypingExtendsWindow proves that an incoming typing indicator
// actually extends the idle window that would otherwise have flushed (PIPE-05).
func TestDebouncer_TypingExtendsWindow(t *testing.T) {
	flushed := make(chan string, 5)
	debouncer := NewSmartDebouncer(DebouncerConfig{
		IdleTimeout: 120 * time.Millisecond,
		OnFlush: func(ctx context.Context, conversationID string) error {
			flushed <- conversationID
			return nil
		},
	})
	defer func() {
		require.NoError(t, debouncer.Close())
	}()

	const convID = "conv-typing-window"

	// Start the idle countdown with an inbound message (flush due at t=120ms).
	debouncer.RecordEvent(convID, EventInboundMessage)
	require.Equal(t, 1, debouncer.ActiveCount())

	// At t=80ms a participant starts typing: the window must extend to t=200ms.
	time.Sleep(80 * time.Millisecond)
	debouncer.RecordTypingType(convID, gmproto.TypingTypes_STARTED_TYPING)

	// At t=160ms the original (un-extended) deadline has passed. Without the
	// typing extension the callback would already have fired.
	time.Sleep(80 * time.Millisecond)
	select {
	case conv := <-flushed:
		t.Fatalf("typing indicator failed to extend the idle window; flushed at 160ms for %s", conv)
	default:
	}

	// The window must still close after silence following the typing event.
	select {
	case conv := <-flushed:
		assert.Equal(t, convID, conv)
	case <-time.After(300 * time.Millisecond):
		t.Fatal("expected flush callback after typing stopped and extended window elapsed")
	}

	assert.Equal(t, 0, debouncer.ActiveCount())
}

// TestDebouncer_TypingStoppedDoesNotReset verifies STOPPED_TYPING doesn't extend the timer.
func TestDebouncer_TypingStoppedDoesNotReset(t *testing.T) {
	flushed := make(chan string, 5)
	debouncer := NewSmartDebouncer(DebouncerConfig{
		IdleTimeout: 100 * time.Millisecond,
		OnFlush: func(ctx context.Context, conversationID string) error {
			flushed <- conversationID
			return nil
		},
	})
	defer func() {
		require.NoError(t, debouncer.Close())
	}()

	debouncer.RecordEvent("conv-stop", EventTypingStarted)

	time.Sleep(30 * time.Millisecond)
	// STOPPED_TYPING shouldn't reset timer
	debouncer.RecordTypingType("conv-stop", gmproto.TypingTypes_STOPPED_TYPING)

	// Remaining time ~70ms. Wait 90ms.
	time.Sleep(90 * time.Millisecond)

	select {
	case conv := <-flushed:
		assert.Equal(t, "conv-stop", conv)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Expected flush callback to fire despite stopped typing")
	}
}

// TestDebouncer_ConcurrentResets tests thread safety with concurrent event emissions.
func TestDebouncer_ConcurrentResets(t *testing.T) {
	var flushCount int64
	debouncer := NewSmartDebouncer(DebouncerConfig{
		IdleTimeout: 50 * time.Millisecond,
		OnFlush: func(ctx context.Context, conversationID string) error {
			atomic.AddInt64(&flushCount, 1)
			return nil
		},
	})

	const numGoroutines = 10
	const numEventsPerGoroutine = 50
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(gID int) {
			defer wg.Done()
			for j := 0; j < numEventsPerGoroutine; j++ {
				convID := "conv-shared"
				if gID%2 == 0 {
					debouncer.RecordEvent(convID, EventTypingStarted)
				} else {
					debouncer.RecordEvent(convID, EventOutboundMessage)
				}
				time.Sleep(1 * time.Millisecond)
			}
		}(i)
	}

	wg.Wait()

	// After all concurrent events finish, wait for debounce to elapse
	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, int64(1), atomic.LoadInt64(&flushCount))

	require.NoError(t, debouncer.Close())
}

// TestDebouncer_CloseCancelsAllTimers verifies clean shutdown without goroutine leaks or late flushes.
func TestDebouncer_CloseCancelsAllTimers(t *testing.T) {
	var flushCount int64
	debouncer := NewSmartDebouncer(DebouncerConfig{
		IdleTimeout: 80 * time.Millisecond,
		OnFlush: func(ctx context.Context, conversationID string) error {
			atomic.AddInt64(&flushCount, 1)
			return nil
		},
	})

	debouncer.RecordEvent("conv-a", EventInboundMessage)
	debouncer.RecordEvent("conv-b", EventTypingStarted)
	debouncer.RecordEvent("conv-c", EventOutboundMessage)
	assert.Equal(t, 3, debouncer.ActiveCount())

	// Close immediately before timeout fires
	time.Sleep(20 * time.Millisecond)
	err := debouncer.Close()
	require.NoError(t, err)

	assert.Equal(t, 0, debouncer.ActiveCount())

	// Wait past the timeout duration
	time.Sleep(120 * time.Millisecond)
	assert.Equal(t, int64(0), atomic.LoadInt64(&flushCount), "No callbacks should fire after Close()")

	// Calling Close() again is safe and idempotent
	assert.NoError(t, debouncer.Close())

	// Recording events after Close is a no-op
	debouncer.RecordEvent("conv-a", EventInboundMessage)
	assert.Equal(t, 0, debouncer.ActiveCount())
}

// TestDebouncer_EmptyConvIDNoop verifies empty conversation IDs are handled safely.
func TestDebouncer_EmptyConvIDNoop(t *testing.T) {
	debouncer := NewSmartDebouncer(DebouncerConfig{
		IdleTimeout: 50 * time.Millisecond,
	})
	defer debouncer.Close()

	debouncer.RecordEvent("", EventInboundMessage)
	assert.Equal(t, 0, debouncer.ActiveCount())
}
