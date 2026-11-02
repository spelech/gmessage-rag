package config_test

import (
	"sync"
	"testing"
	"time"

	"github.com/spelech/gmessage-rag/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRuntimeTuner_Defaults verifies default fallback handling when zero values are provided.
func TestRuntimeTuner_Defaults(t *testing.T) {
	tuner := config.NewRuntimeTuner(config.TunerSettings{})
	settings := tuner.Get()

	assert.Equal(t, config.DefaultDebouncerIdleMinutes, settings.DebouncerIdleMinutes)
	assert.Equal(t, config.DefaultRRFKFactor, settings.RRFKFactor)
	assert.Equal(t, config.DefaultOCREndpoint, settings.OCREndpoint)
	assert.Equal(t, config.DefaultBackfillBatchSize, settings.BackfillBatchSize)
}

// TestRuntimeTuner_CustomInitial verifies custom initial settings are retained.
func TestRuntimeTuner_CustomInitial(t *testing.T) {
	initial := config.TunerSettings{
		DebouncerIdleMinutes: 45,
		RRFKFactor:           80,
		OCREndpoint:          "https://custom-ocr.local/v1",
		BackfillBatchSize:    250,
	}
	tuner := config.NewRuntimeTuner(initial)
	settings := tuner.Get()

	assert.Equal(t, 45, settings.DebouncerIdleMinutes)
	assert.Equal(t, 80, settings.RRFKFactor)
	assert.Equal(t, "https://custom-ocr.local/v1", settings.OCREndpoint)
	assert.Equal(t, 250, settings.BackfillBatchSize)
}

// TestRuntimeTuner_Validation verifies input validation on update.
func TestRuntimeTuner_Validation(t *testing.T) {
	tuner := config.NewRuntimeTuner(config.TunerSettings{})

	// Test invalid DebouncerIdleMinutes
	err := tuner.Update(config.TunerSettings{
		DebouncerIdleMinutes: 0,
		RRFKFactor:           60,
		OCREndpoint:          "https://api.ocr.com",
		BackfillBatchSize:    100,
	})
	assert.ErrorIs(t, err, config.ErrInvalidDebouncerIdleMinutes)

	// Test invalid RRFKFactor
	err = tuner.Update(config.TunerSettings{
		DebouncerIdleMinutes: 15,
		RRFKFactor:           -5,
		OCREndpoint:          "https://api.ocr.com",
		BackfillBatchSize:    100,
	})
	assert.ErrorIs(t, err, config.ErrInvalidRRFKFactor)

	// Test invalid OCREndpoint
	err = tuner.Update(config.TunerSettings{
		DebouncerIdleMinutes: 15,
		RRFKFactor:           50,
		OCREndpoint:          "   ",
		BackfillBatchSize:    100,
	})
	assert.ErrorIs(t, err, config.ErrInvalidOCREndpoint)

	// Test invalid BackfillBatchSize
	err = tuner.Update(config.TunerSettings{
		DebouncerIdleMinutes: 15,
		RRFKFactor:           50,
		OCREndpoint:          "https://api.ocr.com",
		BackfillBatchSize:    0,
	})
	assert.ErrorIs(t, err, config.ErrInvalidBackfillBatchSize)

	// Verify settings did not change on failed validations
	settings := tuner.Get()
	assert.Equal(t, config.DefaultDebouncerIdleMinutes, settings.DebouncerIdleMinutes)
}

// TestRuntimeTuner_SubscribeAndBroadcast verifies that updates broadcast to multiple subscribers.
func TestRuntimeTuner_SubscribeAndBroadcast(t *testing.T) {
	tuner := config.NewRuntimeTuner(config.TunerSettings{})

	sub1 := tuner.Subscribe()
	sub2 := tuner.Subscribe()
	defer tuner.Unsubscribe(sub1)
	defer tuner.Unsubscribe(sub2)

	newSettings := config.TunerSettings{
		DebouncerIdleMinutes: 20,
		RRFKFactor:           50,
		OCREndpoint:          "https://new-ocr.example.com",
		BackfillBatchSize:    300,
	}

	err := tuner.Update(newSettings)
	require.NoError(t, err)

	select {
	case received := <-sub1:
		assert.Equal(t, newSettings, received)
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for update on subscriber 1")
	}

	select {
	case received := <-sub2:
		assert.Equal(t, newSettings, received)
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for update on subscriber 2")
	}

	// Verify unregistering stops notifications
	tuner.Unsubscribe(sub1)
	err = tuner.Update(config.TunerSettings{
		DebouncerIdleMinutes: 25,
		RRFKFactor:           55,
		OCREndpoint:          "https://another.example.com",
		BackfillBatchSize:    350,
	})
	require.NoError(t, err)

	select {
	case received := <-sub1:
		t.Fatalf("unexpected value received on unsubscribed sub1: %+v", received)
	case <-time.After(100 * time.Millisecond):
		// Expected: no notification sent to unsubscribed channel
	}
}

// TestRuntimeTuner_ConcurrentUpdatesAndNotification executes concurrent reads and updates under race detector.
func TestRuntimeTuner_ConcurrentUpdatesAndNotification(t *testing.T) {
	initial := config.TunerSettings{
		DebouncerIdleMinutes: 30,
		RRFKFactor:           60,
		OCREndpoint:          "https://initial-ocr.example.com",
		BackfillBatchSize:    100,
	}
	tuner := config.NewRuntimeTuner(initial)

	sub := tuner.Subscribe()
	defer tuner.Unsubscribe(sub)

	const numGoroutines = 20
	const iterations = 50
	var wg sync.WaitGroup
	wg.Add(numGoroutines * 2)

	// Goroutines performing concurrent reads
	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				s := tuner.Get()
				assert.NotEmpty(t, s.OCREndpoint)
				assert.Greater(t, s.DebouncerIdleMinutes, 0)
			}
		}()
	}

	// Goroutines performing concurrent valid updates
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				_ = tuner.Update(config.TunerSettings{
					DebouncerIdleMinutes: 10 + (id % 5),
					RRFKFactor:           40 + (j % 10),
					OCREndpoint:          "https://ocr.example.com/api",
					BackfillBatchSize:    100 + j,
				})
			}
		}(i)
	}

	// Consumer goroutine draining subscriber updates
	stopConsumer := make(chan struct{})
	go func() {
		for {
			select {
			case <-sub:
			case <-stopConsumer:
				return
			}
		}
	}()

	wg.Wait()
	close(stopConsumer)

	final := tuner.Get()
	assert.Greater(t, final.DebouncerIdleMinutes, 0)
	assert.Greater(t, final.RRFKFactor, 0)
	assert.Equal(t, "https://ocr.example.com/api", final.OCREndpoint)
}
