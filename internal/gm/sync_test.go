package gm

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeConnector records the connect/disconnect sequence without a network.
type fakeConnector struct {
	mu        sync.Mutex
	loggedIn  bool
	connected bool
	events    []string
}

func (f *fakeConnector) IsLoggedIn() bool { return f.loggedIn }

func (f *fakeConnector) IsConnected() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.connected
}

func (f *fakeConnector) Connect(_ context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.connected = true
	f.events = append(f.events, "connect")
	return nil
}

func (f *fakeConnector) Disconnect() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.connected = false
	f.events = append(f.events, "disconnect")
}

// TestPeriodicSyncLoop_ConnectDrainDisconnect verifies SYNC-01: one round
// connects, runs the drain callback, then disconnects, and returns on cancel.
func TestPeriodicSyncLoop_ConnectDrainDisconnect(t *testing.T) {
	f := &fakeConnector{loggedIn: true}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	var drained int
	go func() {
		PeriodicSyncLoop(ctx, f, PeriodicSyncConfig{
			Interval: time.Hour,
			Window:   50 * time.Millisecond,
			Logger:   zerolog.Nop(),
			OnWindow: func(context.Context) {
				drained++
				cancel()
			},
		})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("PeriodicSyncLoop did not return after cancel")
	}

	require.Equal(t, 1, drained, "drain callback should run once per window")
	assert.Equal(t, []string{"connect", "disconnect"}, f.events)
}

// TestPeriodicSyncLoop_NotLoggedInWaits verifies the loop does not connect
// without a paired session and stops promptly on cancellation.
func TestPeriodicSyncLoop_NotLoggedInWaits(t *testing.T) {
	f := &fakeConnector{loggedIn: false}
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		PeriodicSyncLoop(ctx, f, PeriodicSyncConfig{
			Interval: time.Hour,
			Window:   time.Minute,
			Logger:   zerolog.Nop(),
		})
		close(done)
	}()

	time.Sleep(30 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("PeriodicSyncLoop did not return after cancel")
	}
	assert.Empty(t, f.events, "no connect/disconnect without a session")
}

// TestPeriodicSyncLoop_DefaultsAndCancel verifies a nil connector is a no-op and
// a zero-valued config does not panic before cancellation.
func TestPeriodicSyncLoop_DefaultsAndCancel(t *testing.T) {
	PeriodicSyncLoop(context.Background(), nil, PeriodicSyncConfig{})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f := &fakeConnector{loggedIn: true}
	PeriodicSyncLoop(ctx, f, PeriodicSyncConfig{Logger: zerolog.Nop()})
	assert.Empty(t, f.events)
}
