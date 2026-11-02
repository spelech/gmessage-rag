package gm

import (
	"context"
	"fmt"
)

// CurrentEmoji returns the current Gaia pairing emoji challenge thread-safely.
func (c *Client) CurrentEmoji() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.currentEmoji
}

// PairGaia performs single-device Google Account (Gaia) emoji challenge pairing (AUTH-04).
// It stores the challenge emoji thread-safely, invokes the onEmoji callback when emitted,
// and saves session credentials with POSIX 0600 permissions upon pairing success.
func (c *Client) PairGaia(ctx context.Context, onEmoji func(emoji string)) error {
	emojiCallback := func(emoji string) {
		c.mu.Lock()
		c.currentEmoji = emoji
		c.mu.Unlock()

		if onEmoji != nil {
			onEmoji(emoji)
		}
	}

	if err := c.libgmClient.DoGaiaPairing(ctx, emojiCallback); err != nil {
		return fmt.Errorf("gaia pairing failed: %w", err)
	}

	if err := SaveAuthData(c.sessionPath, c.authData); err != nil {
		return fmt.Errorf("failed to save auth data after gaia pairing: %w", err)
	}

	return nil
}
