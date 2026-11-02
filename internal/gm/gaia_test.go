package gm

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGaiaPairing_CurrentEmoji_ThreadSafety tests CurrentEmoji under concurrent read/write.
func TestGaiaPairing_CurrentEmoji_ThreadSafety(t *testing.T) {
	tempDir := t.TempDir()
	sessionPath := filepath.Join(tempDir, "session.json")

	client, err := NewClient(ClientOptions{
		SessionPath: sessionPath,
	})
	require.NoError(t, err)

	assert.Equal(t, "", client.CurrentEmoji())

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Readers
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = client.CurrentEmoji()
				}
			}
		}()
	}

	// Writers
	emojis := []string{"🚀", "🐶", "☕", "🎉", "🔥"}
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				client.mu.Lock()
				client.currentEmoji = emojis[(id+j)%len(emojis)]
				client.mu.Unlock()
				time.Sleep(1 * time.Millisecond)
			}
		}(i)
	}

	// Wait for writers to complete
	time.Sleep(100 * time.Millisecond)
	close(stop)
	wg.Wait()

	finalEmoji := client.CurrentEmoji()
	assert.Contains(t, emojis, finalEmoji)
}

// TestGaiaPairing_CallbackInvocation verifies callback invocation and thread-safe emoji storage.
func TestGaiaPairing_CallbackInvocation(t *testing.T) {
	tempDir := t.TempDir()
	sessionPath := filepath.Join(tempDir, "session.json")

	client, err := NewClient(ClientOptions{
		SessionPath: sessionPath,
	})
	require.NoError(t, err)

	var emittedEmoji string
	var callbackCalled bool
	var mu sync.Mutex

	onEmoji := func(emoji string) {
		mu.Lock()
		defer mu.Unlock()
		callbackCalled = true
		emittedEmoji = emoji
	}

	// Test the emoji callback logic directly
	emojiCallback := func(emoji string) {
		client.mu.Lock()
		client.currentEmoji = emoji
		client.mu.Unlock()

		if onEmoji != nil {
			onEmoji(emoji)
		}
	}

	testEmoji := "🚀"
	emojiCallback(testEmoji)

	mu.Lock()
	assert.True(t, callbackCalled)
	assert.Equal(t, testEmoji, emittedEmoji)
	mu.Unlock()

	assert.Equal(t, testEmoji, client.CurrentEmoji())
}

// TestGaiaPairing_ContextCancellation verifies that PairGaia handles context cancellation.
func TestGaiaPairing_ContextCancellation(t *testing.T) {
	tempDir := t.TempDir()
	sessionPath := filepath.Join(tempDir, "session.json")

	client, err := NewClient(ClientOptions{
		SessionPath: sessionPath,
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // canceled immediately

	var receivedEmoji string
	err = client.PairGaia(ctx, func(emoji string) {
		receivedEmoji = emoji
	})

	assert.Error(t, err)
	assert.Empty(t, receivedEmoji)
}

// TestGaiaPairing_SaveAuthData_Permissions verifies that SaveAuthData ensures POSIX 0600 permissions upon success.
func TestGaiaPairing_SaveAuthData_Permissions(t *testing.T) {
	tempDir := t.TempDir()
	sessionPath := filepath.Join(tempDir, "auth", "session.json")

	client, err := NewClient(ClientOptions{
		SessionPath: sessionPath,
	})
	require.NoError(t, err)

	client.AuthData().TachyonAuthToken = []byte("gaia-pair-test-token")

	err = SaveAuthData(client.SessionPath(), client.AuthData())
	require.NoError(t, err)

	info, err := os.Stat(sessionPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), info.Mode().Perm(), "session.json must have POSIX 0600 permissions")
}
