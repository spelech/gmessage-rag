package notify

import (
	"context"
	"math"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

// WatchdogConfig configures the connection watchdog.
type WatchdogConfig struct {
	NotifyClient            *Client
	NotifyOnPhoneDisconnect bool
	GracePeriod             time.Duration
	Logger                  zerolog.Logger
}

// Watchdog monitors phone connection events and triggers debounced alerts.
// Requirement ID: TRANS-04, AUTH-03
type Watchdog struct {
	notifyClient            *Client
	notifyOnPhoneDisconnect bool
	gracePeriod             time.Duration
	logger                  zerolog.Logger

	mu         sync.Mutex
	timer      *time.Timer
	timerStart time.Time
	cancelCh   chan struct{}
}

// NewWatchdog initializes a new Watchdog instance.
func NewWatchdog(cfg WatchdogConfig) *Watchdog {
	grace := cfg.GracePeriod
	if grace <= 0 {
		grace = 15 * time.Minute
	}

	return &Watchdog{
		notifyClient:            cfg.NotifyClient,
		notifyOnPhoneDisconnect: cfg.NotifyOnPhoneDisconnect,
		gracePeriod:             grace,
		logger:                  cfg.Logger,
	}
}

// OnPhoneNotResponding handles PhoneNotResponding events by starting a debounced cancellable grace timer.
func (w *Watchdog) OnPhoneNotResponding() {
	w.mu.Lock()
	defer w.mu.Unlock()

	if !w.notifyOnPhoneDisconnect {
		w.logger.Debug().Msg("Phone not responding, but disconnect notifications disabled")
		return
	}

	if w.timer != nil {
		// Timer already pending
		w.logger.Debug().Msg("Phone not responding timer already running")
		return
	}

	w.logger.Info().Dur("grace_period", w.gracePeriod).Msg("Phone not responding, starting grace period timer")
	cancelCh := make(chan struct{})
	w.cancelCh = cancelCh
	w.timerStart = time.Now()

	w.timer = time.AfterFunc(w.gracePeriod, func() {
		w.mu.Lock()
		w.timer = nil
		w.cancelCh = nil
		w.mu.Unlock()

		w.logger.Warn().Dur("grace_period", w.gracePeriod).Msg("Phone disconnect grace period elapsed; sending alert")
		if w.notifyClient != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			graceMins := int(math.Round(w.gracePeriod.Minutes()))
			if err := w.notifyClient.SendPhoneDisconnectedAlert(ctx, graceMins); err != nil {
				w.logger.Error().Err(err).Msg("Failed to dispatch phone disconnect alert")
			}
		}
	})
}

// OnPhoneRespondingAgain cancels any pending disconnect timer without alerting.
func (w *Watchdog) OnPhoneRespondingAgain() {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.timer != nil {
		w.timer.Stop()
		w.timer = nil
		if w.cancelCh != nil {
			close(w.cancelCh)
			w.cancelCh = nil
		}
		w.logger.Info().Msg("Phone responded again; cancelled disconnect grace timer")
	}
}

// OnListenFatalError dispatches an immediate session re-auth alert via notifyClient.
func (w *Watchdog) OnListenFatalError(err error) {
	w.logger.Error().Err(err).Msg("Listen fatal error received by watchdog; sending session re-auth alert")
	if w.notifyClient != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if sendErr := w.notifyClient.SendSessionExpiredAlert(ctx); sendErr != nil {
			w.logger.Error().Err(sendErr).Msg("Failed to send session expired alert on ListenFatalError")
		}
	}
}

// IsTimerActive checks whether a disconnect timer is currently running (useful for unit testing).
func (w *Watchdog) IsTimerActive() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.timer != nil
}
