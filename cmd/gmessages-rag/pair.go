package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/rs/zerolog"
	"github.com/spf13/cobra"

	"github.com/spelech/gmessage-rag/internal/gm"
)

var (
	cookiesFlag     string
	pairSessionFlag string
)

const cookieInstructions = `Paste your Google session cookies below, then press Enter on an empty line (or Ctrl-D).

Easiest method:
  1. Sign in to https://messages.google.com/web in your browser.
  2. Open DevTools (F12) -> Network tab.
  3. Select any request to messages.google.com (for example "config").
  4. Right-click the request -> Copy -> Copy as cURL.
  5. Paste the whole cURL command here.
  (Or save to a file and run: ./bin/gmessages-rag pair --cookies /path/to/file)

JSON cookie maps and raw "Cookie: ..." headers are also accepted.
`

// runPair pairs Google Messages by importing Google session cookies and then
// starting the Gaia emoji challenge. Device pairing uses cookie import.
func runPair(cmd *cobra.Command, args []string) error {
	parentCtx := cmd.Context()
	if parentCtx == nil {
		parentCtx = context.Background()
	}
	ctx, cancel := signal.NotifyContext(parentCtx, os.Interrupt, syscall.SIGTERM)
	defer cancel()

	logger := zerolog.New(os.Stderr).With().Timestamp().Logger()

	sessionPath := os.Getenv("SESSION_PATH")
	if sessionPath == "" {
		if pairSessionFlag != "" {
			sessionPath = pairSessionFlag
		} else if _, err := os.Stat("data/auth"); err == nil {
			sessionPath = "data/auth/session.json"
		} else if _, err := os.Stat("data/auth/session.json"); err == nil {
			sessionPath = "data/auth/session.json"
		} else {
			sessionPath = gm.DefaultSessionPath
		}
	}

	rawCookies, err := readCookieInput(cookiesFlag)
	if err != nil {
		return err
	}

	cookies, err := gm.ParseCookies(rawCookies)
	if err != nil {
		return fmt.Errorf("invalid cookies: %w", err)
	}

	client, err := gm.NewClient(gm.ClientOptions{
		SessionPath: sessionPath,
		Logger:      logger.With().Str("component", "pairing").Logger(),
	})
	if err != nil {
		return fmt.Errorf("failed to initialize pairing client: %w", err)
	}

	err = client.PairWithCookies(ctx, cookies, func(emoji string) {
		fmt.Println("\n=======================================================")
		fmt.Printf(" [!] GAIA PAIRING CHALLENGE EMOJI: %s\n", emoji)
		fmt.Println(" Open Google Messages on your phone, go to Device Pairing,")
		fmt.Println(" and tap the matching emoji.")
		fmt.Println("=======================================================")
	})
	if err != nil {
		return fmt.Errorf("cookie pairing error: %w", err)
	}

	fmt.Printf("Pairing successful! Session persisted to %s\n", sessionPath)
	return nil
}

// readCookieInput resolves the --cookies value. An empty value or "-" reads
// from stdin. An existing file path is read from disk. Anything else is treated
// as inline cookie text (JSON, cURL, or a raw Cookie header).
func readCookieInput(source string) (string, error) {
	if source == "" || source == "-" {
		fmt.Fprint(os.Stderr, cookieInstructions)
		scanner := bufio.NewScanner(os.Stdin)
		buf := make([]byte, 64*1024)
		const maxCapacity = 10 * 1024 * 1024
		scanner.Buffer(buf, maxCapacity)

		var sb strings.Builder
		for scanner.Scan() {
			line := scanner.Text()
			if strings.TrimSpace(line) == "" && sb.Len() > 0 {
				break
			}
			sb.WriteString(line)
			sb.WriteString("\n")
		}
		if err := scanner.Err(); err != nil {
			return "", fmt.Errorf("failed to read cookies from stdin: %w", err)
		}
		res := strings.TrimSpace(sb.String())
		if res == "" {
			return "", fmt.Errorf("no cookie input received")
		}
		return res, nil
	}

	info, statErr := os.Stat(source)
	if statErr == nil && !info.IsDir() {
		data, err := os.ReadFile(source)
		if err != nil {
			return "", fmt.Errorf("failed to read cookie file %s: %w", source, err)
		}
		return string(data), nil
	}

	if statErr != nil && !os.IsNotExist(statErr) {
		return "", fmt.Errorf("failed to inspect cookie source %s: %w", source, statErr)
	}

	if strings.ContainsAny(source, "{[=;") || strings.Contains(strings.ToLower(source), "curl") || strings.Contains(strings.ToLower(source), "cookie:") {
		return source, nil
	}

	return "", fmt.Errorf("cookie file not found: %s", source)
}
