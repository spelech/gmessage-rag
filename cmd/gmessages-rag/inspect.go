package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"

	"github.com/spelech/gmessage-rag/internal/gm"
)

var inspectCmd = &cobra.Command{
	Use:   "inspect",
	Short: "Inspect Google Messages login, list active conversations, and print sample messages without saving to DB",
	Long: `Connects to Google Messages using saved session credentials, tests login status,
fetches the conversation list and contacts, and inspects recent message payloads.
No data is written to PostgreSQL or disk.`,
	RunE: runInspect,
}

var (
	inspectLimit   int64
	inspectConvID  string
	inspectSession string
)

func init() {
	inspectCmd.Flags().Int64Var(&inspectLimit, "limit", 10, "Maximum number of conversations or messages to list")
	inspectCmd.Flags().StringVar(&inspectConvID, "conv", "", "Inspect messages from a specific conversation ID")
	inspectCmd.Flags().StringVar(&inspectSession, "session", "", "Path to session.json (defaults to data/auth/session.json or SESSION_PATH)")
	rootCmd.AddCommand(inspectCmd)
}

func runInspect(cmd *cobra.Command, args []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	logger := log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339}).With().Str("component", "inspect").Logger()

	sessionPath := inspectSession
	if sessionPath == "" {
		sessionPath = os.Getenv("SESSION_PATH")
	}
	if sessionPath == "" {
		if _, err := os.Stat("data/auth/session.json"); err == nil {
			sessionPath = "data/auth/session.json"
		} else {
			sessionPath = "/data/auth/session.json"
		}
	}

	logger.Info().Str("session_path", sessionPath).Msg("Loading Google Messages session...")
	client, err := gm.NewClient(gm.ClientOptions{
		SessionPath: sessionPath,
		Logger:      logger,
	})
	if err != nil {
		return fmt.Errorf("failed to load session from %s: %w", sessionPath, err)
	}

	if !client.LibGM().IsLoggedIn() {
		return fmt.Errorf("session at %s is not logged in / paired", sessionPath)
	}

	logger.Info().Msg("Session credentials present and valid. Connecting to Google Messages...")
	if err := client.Connect(ctx); err != nil {
		return fmt.Errorf("connect failed: %w", err)
	}

	// Wait 2 seconds for initial connection handshake
	time.Sleep(2 * time.Second)

	fmt.Println("\n=======================================================")
	fmt.Println("  Google Messages Connection Status: CONNECTED")
	fmt.Println("=======================================================")

	// 1. Fetch Google Contacts roster
	logger.Info().Msg("Fetching Google Contacts roster...")
	contacts, err := gm.SyncContacts(ctx, client)
	if err != nil {
		logger.Warn().Err(err).Msg("Failed to fetch Google Contacts")
	} else {
		fmt.Printf("\n[+] Found %d Google Contacts:\n", len(contacts))
		displayCount := len(contacts)
		if displayCount > int(inspectLimit) {
			displayCount = int(inspectLimit)
		}
		for i := 0; i < displayCount; i++ {
			c := contacts[i]
			fmt.Printf("    - %s (%s) | ContactID: %s | ParticipantID: %s\n", c.Name, c.Number, c.ContactID, c.ParticipantID)
		}
		if len(contacts) > displayCount {
			fmt.Printf("    ... and %d more contacts\n", len(contacts)-displayCount)
		}
	}

	// 2. Fetch Conversations list
	logger.Info().Int64("limit", inspectLimit).Msg("Fetching conversation list...")
	convResp, err := client.ListConversations(ctx, inspectLimit)
	if err != nil {
		return fmt.Errorf("failed to list conversations: %w", err)
	}

	rawConvs := convResp.GetConversations()
	fmt.Printf("\n[+] Found %d Conversations (Limit %d):\n", len(rawConvs), inspectLimit)

	var targetConvID string
	for i, c := range rawConvs {
		convID := c.GetConversationID()
		name := c.GetName()
		if name == "" {
			var participantNames []string
			for _, p := range c.GetParticipants() {
				if p.GetFullName() != "" {
					participantNames = append(participantNames, p.GetFullName())
				} else if p.GetFormattedNumber() != "" {
					participantNames = append(participantNames, p.GetFormattedNumber())
				}
			}
			name = strings.Join(participantNames, ", ")
		}
		lastMsgSnippet := ""
		if c.GetLatestMessage() != nil {
			lastMsgSnippet = c.GetLatestMessage().GetDisplayContent()
			if len(lastMsgSnippet) > 40 {
				lastMsgSnippet = lastMsgSnippet[:40] + "..."
			}
		}

		fmt.Printf("  [%2d] ID: %s\n", i+1, convID)
		fmt.Printf("       Name: %s\n", name)
		if lastMsgSnippet != "" {
			fmt.Printf("       Last Message: %q\n", lastMsgSnippet)
		}
		if targetConvID == "" {
			targetConvID = convID
		}
	}

	// 3. Inspect messages from a conversation
	if inspectConvID != "" {
		targetConvID = inspectConvID
	}

	if targetConvID != "" {
		fmt.Printf("\n[+] Inspecting sample messages for conversation: %s\n", targetConvID)
		msgResp, err := client.FetchMessages(ctx, targetConvID, 5, nil)
		if err != nil {
			logger.Warn().Err(err).Str("conv_id", targetConvID).Msg("Failed to fetch messages for conversation")
		} else {
			msgs := msgResp.GetMessages()
			fmt.Printf("    Fetched %d messages:\n", len(msgs))
			for j, m := range msgs {
				sentAt := gm.NormalizeTimestamp(m.GetTimestamp())
				sender := m.GetParticipantID()
				if m.GetSenderParticipant() != nil && m.GetSenderParticipant().GetFullName() != "" {
					sender = m.GetSenderParticipant().GetFullName()
				}
				body := ""
				for _, info := range m.GetMessageInfo() {
					if mc := info.GetMessageContent(); mc != nil {
						body = mc.GetContent()
						break
					}
					if info.GetMediaContent() != nil {
						body = fmt.Sprintf("[MEDIA: %s]", info.GetMediaContent().GetMimeType())
						break
					}
				}
				fmt.Printf("    (%d) [%s] %s: %s\n", j+1, sentAt.Format("2006-01-02 15:04:05"), sender, body)
			}
		}
	}

	fmt.Println("\n=======================================================")
	fmt.Println("  Inspection Complete (0 bytes written to database)")
	fmt.Println("=======================================================")
	return nil
}
