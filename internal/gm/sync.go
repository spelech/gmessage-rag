package gm

import (
	"context"
	"time"

	"github.com/rs/zerolog"
)

// SyncConnector is the connection surface the periodic sync loop drives.
// *Client implements it; tests substitute a fake to assert the connect/window/
// drain/disconnect sequence without a network.
type SyncConnector interface {
	IsLoggedIn() bool
	IsConnected() bool
	Connect(ctx context.Context) error
	Disconnect()
}

// PeriodicSyncConfig configures PeriodicSyncLoop.
type PeriodicSyncConfig struct {
	// Interval is the wall-clock time between the start of one sync round and
	// the next. The connection is only held for Window out of each Interval.
	Interval time.Duration
	// Window is how long the connection stays open each round so queued events
	// can be delivered before OnWindow drains history and we disconnect.
	Window time.Duration
	// Settle is an optional extra hold after the connection reports ready and
	// before OnWindow runs.
	Settle time.Duration
	// Logger receives structured progress logs. A zero value is silenced.
	Logger zerolog.Logger
	// OnWindow runs while connected. It must not block for long; it is expected
	// to drain tracked conversations via FetchMessages and return.
	OnWindow func(ctx context.Context)
}

// PeriodicSyncLoop is the periodic alternative to ReconnectLoop. Each round it
// connects, holds the connection for Window, runs OnWindow to drain messages,
// then disconnects and sleeps until the next Interval.
//
// It owns connect and disconnect, so ReconnectLoop must not run in the same
// process. Disconnecting does not clear the session cookie, so the same
// session.json resumes on the next round.
func PeriodicSyncLoop(ctx context.Context, conn SyncConnector, cfg PeriodicSyncConfig) {
	if conn == nil {
		return
	}
	if cfg.Interval <= 0 {
		cfg.Interval = 30 * time.Minute
	}
	if cfg.Window < 0 {
		cfg.Window = 0
	}
	if cfg.Settle < 0 {
		cfg.Settle = 0
	}
	log := cfg.Logger

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		roundStart := time.Now()

		if !conn.IsLoggedIn() {
			log.Warn().Msg("periodic sync: session not paired; waiting for a cookie import")
			if !sleepCtx(ctx, clampDuration(cfg.Interval, time.Minute)) {
				return
			}
			continue
		}

		if conn.IsConnected() {
			conn.Disconnect()
		}

		if err := conn.Connect(ctx); err != nil {
			log.Warn().Err(err).Msg("periodic sync: connect failed; will retry next interval")
			if !sleepCtx(ctx, clampDuration(cfg.Interval, 5*time.Minute)) {
				return
			}
			continue
		}

		connected := waitConnected(ctx, conn, cfg.Window)
		if connected {
			log.Info().Dur("window", cfg.Window).Msg("periodic sync: connected, holding window")
			if cfg.Settle > 0 && !sleepCtx(ctx, cfg.Settle) {
				conn.Disconnect()
				return
			}
			// Hold the remainder of the window so queued events arrive.
			held := time.Since(roundStart)
			if remaining := cfg.Window - held; remaining > 0 {
				if !sleepCtx(ctx, remaining) {
					conn.Disconnect()
					return
				}
			}
			if cfg.OnWindow != nil {
				cfg.OnWindow(ctx)
			}
		} else {
			log.Warn().Dur("window", cfg.Window).Msg("periodic sync: connection not ready within window; skipping drain")
		}

		conn.Disconnect()
		log.Info().Msg("periodic sync: disconnected until next window")

		if remaining := cfg.Interval - time.Since(roundStart); remaining > 0 {
			if !sleepCtx(ctx, remaining) {
				return
			}
		}
	}
}

// PeriodicSyncLoop is the method form used by the daemon. See the package-level
// function for behavior.
func (c *Client) PeriodicSyncLoop(ctx context.Context, cfg PeriodicSyncConfig) {
	PeriodicSyncLoop(ctx, c, cfg)
}

// waitConnected polls until the connector reports connected or timeout elapses.
func waitConnected(ctx context.Context, conn SyncConnector, timeout time.Duration) bool {
	if conn.IsConnected() {
		return true
	}
	if timeout <= 0 {
		return conn.IsConnected()
	}
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
			if conn.IsConnected() {
				return true
			}
			if time.Now().After(deadline) {
				return false
			}
		}
	}
}

// sleepCtx waits for d or until ctx is cancelled. It reports false on cancel.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		select {
		case <-ctx.Done():
			return false
		default:
			return true
		}
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// clampDuration returns d, or max when d exceeds it.
func clampDuration(d, max time.Duration) time.Duration {
	if d > max {
		return max
	}
	return d
}
