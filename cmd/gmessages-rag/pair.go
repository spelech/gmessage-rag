package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/rs/zerolog"
	"github.com/spf13/cobra"

	"github.com/spelech/gmessage-rag/internal/gm"
)

var (
	qrFlag bool
)

// runPair executes terminal-based pairing (default Gaia emoji pairing, or legacy QR via --qr).
func runPair(cmd *cobra.Command, args []string) error {
	parentCtx := cmd.Context()
	if parentCtx == nil {
		parentCtx = context.Background()
	}
	ctx, cancel := signal.NotifyContext(parentCtx, os.Interrupt, syscall.SIGTERM)
	defer cancel()

	logger := zerolog.New(os.Stderr).With().Timestamp().Logger()

	sessionPath := "/data/auth/session.json"
	// Check if environment specifies a custom auth directory/session path or fallback
	if envPath := os.Getenv("SESSION_PATH"); envPath != "" {
		sessionPath = envPath
	}

	opts := gm.ClientOptions{
		SessionPath: sessionPath,
		Logger:      logger.With().Str("component", "pairing").Logger(),
	}

	if qrFlag {
		fmt.Println("Initiating Google Messages QR terminal pairing session (legacy fallback)...")
		client, err := gm.NewClient(opts)
		if err != nil {
			return fmt.Errorf("failed to initialize pairing client: %w", err)
		}

		if err := client.PairCLI(ctx); err != nil {
			return fmt.Errorf("terminal pairing error: %w", err)
		}

		fmt.Printf("Terminal pairing successful! Session persisted to %s\n", sessionPath)
		return nil
	}

	fmt.Println("Initiating Google Account (Gaia) single-device pairing challenge (AUTH-04, default)...")
	gaiaClient, err := gm.NewGaiaClient(opts)
	if err != nil {
		return fmt.Errorf("failed to initialize gaia client: %w", err)
	}

	err = gaiaClient.PairGaia(ctx, func(emoji string) {
		fmt.Println("\n=======================================================")
		fmt.Printf(" [!] GAIA PAIRING CHALLENGE EMOJI: %s\n", emoji)
		fmt.Println(" Open Google Messages on your phone and select this emoji!")
		fmt.Println("=======================================================")
	})
	if err != nil {
		return fmt.Errorf("gaia pairing error: %w", err)
	}
	fmt.Printf("Gaia pairing successful! Session persisted to %s\n", sessionPath)
	return nil
}
