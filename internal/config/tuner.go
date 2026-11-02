package config

import (
	"errors"
	"strings"
	"sync"
)

// Default tuner parameter values.
const (
	DefaultDebouncerIdleMinutes = 30
	DefaultRRFKFactor           = 60
	DefaultOCREndpoint          = "https://openrouter.ai/api/v1"
	DefaultBackfillBatchSize    = 100
)

var (
	// ErrInvalidDebouncerIdleMinutes is returned when debouncer idle minutes is <= 0.
	ErrInvalidDebouncerIdleMinutes = errors.New("debouncer idle minutes must be greater than 0")
	// ErrInvalidRRFKFactor is returned when RRF k factor is <= 0.
	ErrInvalidRRFKFactor = errors.New("RRF k factor must be greater than 0")
	// ErrInvalidOCREndpoint is returned when OCR endpoint is empty.
	ErrInvalidOCREndpoint = errors.New("OCR endpoint cannot be empty")
	// ErrInvalidBackfillBatchSize is returned when backfill batch size is <= 0.
	ErrInvalidBackfillBatchSize = errors.New("backfill batch size must be greater than 0")
)

// TunerSettings encapsulates hot-reloadable pipeline parameters.
type TunerSettings struct {
	DebouncerIdleMinutes int    `json:"debouncer_idle_minutes"`
	RRFKFactor           int    `json:"rrf_k_factor"`
	OCREndpoint          string `json:"ocr_endpoint"`
	BackfillBatchSize    int    `json:"backfill_batch_size"`
}

// Validate validates that tuner settings contain valid non-zero parameters.
func (s TunerSettings) Validate() error {
	if s.DebouncerIdleMinutes <= 0 {
		return ErrInvalidDebouncerIdleMinutes
	}
	if s.RRFKFactor <= 0 {
		return ErrInvalidRRFKFactor
	}
	if strings.TrimSpace(s.OCREndpoint) == "" {
		return ErrInvalidOCREndpoint
	}
	if s.BackfillBatchSize <= 0 {
		return ErrInvalidBackfillBatchSize
	}
	return nil
}

// RuntimeTuner provides thread-safe runtime parameter updates and event subscriptions.
type RuntimeTuner struct {
	mu          sync.RWMutex
	settings    TunerSettings
	subscribers map[chan TunerSettings]struct{}
}

// NewRuntimeTuner initializes a new RuntimeTuner with the given initial settings.
// If any initial settings are invalid or empty, sensible defaults are applied.
func NewRuntimeTuner(initial TunerSettings) *RuntimeTuner {
	if initial.DebouncerIdleMinutes <= 0 {
		initial.DebouncerIdleMinutes = DefaultDebouncerIdleMinutes
	}
	if initial.RRFKFactor <= 0 {
		initial.RRFKFactor = DefaultRRFKFactor
	}
	if strings.TrimSpace(initial.OCREndpoint) == "" {
		initial.OCREndpoint = DefaultOCREndpoint
	} else {
		initial.OCREndpoint = strings.TrimSpace(initial.OCREndpoint)
	}
	if initial.BackfillBatchSize <= 0 {
		initial.BackfillBatchSize = DefaultBackfillBatchSize
	}

	return &RuntimeTuner{
		settings:    initial,
		subscribers: make(map[chan TunerSettings]struct{}),
	}
}

// Get returns a thread-safe snapshot of the current tuner settings.
func (t *RuntimeTuner) Get() TunerSettings {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.settings
}

// Update validates and thread-safely updates tuner settings, broadcasting the new settings
// to all registered subscribers without blocking.
func (t *RuntimeTuner) Update(newSettings TunerSettings) error {
	if err := newSettings.Validate(); err != nil {
		return err
	}
	newSettings.OCREndpoint = strings.TrimSpace(newSettings.OCREndpoint)

	t.mu.Lock()
	t.settings = newSettings
	// Snapshot subscriber channels while holding the write lock
	subs := make([]chan TunerSettings, 0, len(t.subscribers))
	for ch := range t.subscribers {
		subs = append(subs, ch)
	}
	t.mu.Unlock()

	// Broadcast to subscribers non-blockingly
	for _, ch := range subs {
		select {
		case ch <- newSettings:
		default:
			// If buffer is full, drain oldest to ensure subscriber gets freshest settings
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- newSettings:
			default:
			}
		}
	}

	return nil
}

// Subscribe registers a new subscriber channel and returns a receive-only channel.
// Updates will be published to the channel when parameters change.
func (t *RuntimeTuner) Subscribe() <-chan TunerSettings {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Buffer size of 10 ensures typical consumer loops don't lag behind
	ch := make(chan TunerSettings, 10)
	t.subscribers[ch] = struct{}{}
	return ch
}

// Unsubscribe unregisters a subscriber channel from future updates.
func (t *RuntimeTuner) Unsubscribe(sub <-chan TunerSettings) {
	t.mu.Lock()
	defer t.mu.Unlock()

	for ch := range t.subscribers {
		if (<-chan TunerSettings)(ch) == sub {
			delete(t.subscribers, ch)
			return
		}
	}
}
