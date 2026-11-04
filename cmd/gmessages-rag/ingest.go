package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"

	"github.com/spelech/gmessage-rag/internal/config"
	"github.com/spelech/gmessage-rag/internal/db"
	"github.com/spelech/gmessage-rag/internal/gm"
	"github.com/spelech/gmessage-rag/internal/pipeline"
)

var ingestCmd = &cobra.Command{
	Use:   "ingest",
	Short: "Ingest Google Messages conversation into PostgreSQL database",
	Long: `Ingests messages from a specific Google Messages conversation (e.g. Molly Madden)
into PostgreSQL, runs the ETL pipeline, sessionizes messages, and updates the Living Dossier.`,
	RunE: runIngest,
}

var (
	ingestConvID        string
	ingestContact       string
	ingestLimit         int64
	ingestSession       string
	ingestDBURL         string
	ingestSkipLLM       bool
	ingestReconcileOnly bool
)

func init() {
	ingestCmd.Flags().StringVar(&ingestConvID, "conv", "", "Conversation ID to ingest (e.g. 12)")
	ingestCmd.Flags().StringVar(&ingestContact, "contact", "Molly Madden", "Contact name to resolve if --conv is not provided")
	ingestCmd.Flags().Int64Var(&ingestLimit, "limit", 50, "Maximum number of messages to ingest")
	ingestCmd.Flags().StringVar(&ingestSession, "session", "", "Path to session.json (defaults to data/auth/session.json or SESSION_PATH)")
	ingestCmd.Flags().StringVar(&ingestDBURL, "db", "", "PostgreSQL database URL")
	ingestCmd.Flags().BoolVar(&ingestSkipLLM, "skip-llm", false, "Skip LLM sessionization and Living Dossier reconciliation")
	ingestCmd.Flags().BoolVar(&ingestReconcileOnly, "reconcile-only", false, "Reconcile Living Dossier from existing database sessions without connecting to Google Messages")
	rootCmd.AddCommand(ingestCmd)
}

func runIngest(cmd *cobra.Command, args []string) error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	logger := log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339}).With().Str("component", "ingest").Logger()

	// 1. Resolve session path
	sessionPath := ingestSession
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

	// 2. Resolve database URL
	dbURL := ingestDBURL
	if dbURL == "" {
		dbURL = os.Getenv("DATABASE_URL")
	}
	if dbURL == "" {
		dbURL = "postgres://rcsrag:changeme@127.0.0.1:5432/gmessages?sslmode=disable"
	} else {
		// If running on host, substitute docker service name 'db' with 127.0.0.1
		dbURL = strings.Replace(dbURL, "@db:5432", "@127.0.0.1:5432", 1)
	}

	logger.Info().Str("session_path", sessionPath).Str("db_url", dbURL).Msg("Connecting to PostgreSQL database...")
	database, err := db.New(ctx, dbURL)
	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}
	defer database.Close()

	logger.Info().Msg("Applying embedded database migrations...")
	if err := database.Migrate(ctx); err != nil {
		return fmt.Errorf("failed to apply migrations: %w", err)
	}

	if ingestReconcileOnly {
		targetConvID := ingestConvID
		if targetConvID == "" {
			targetConvID = "12"
		}
		cfg, _ := config.LoadConfig()
		llmBase := cfg.LLMBaseURL
		if strings.Contains(llmBase, "litellm:4000") {
			llmBase = "http://localhost:8448"
		}
		dossierDir := "./data/dossiers"
		_ = os.MkdirAll(dossierDir, 0755)
		dossierModel := pipeline.NewOpenAIDossierModel(pipeline.DossierModelConfig{
			BaseURL: llmBase,
			APIKey:  cfg.LLMAPIKey,
			Model:   cfg.DossierModel,
			Logger:  logger.With().Str("component", "dossier_model").Logger(),
		})
		dossierReconciler := pipeline.NewDossierReconciler(pipeline.DossierReconcilerConfig{
			Store:     database,
			Model:     dossierModel,
			OutputDir: dossierDir,
			Logger:    logger.With().Str("component", "dossier").Logger(),
		})
		logger.Info().Str("conversation_id", targetConvID).Msg("Reconciling Living Dossier from existing sessions...")
		outcome, err := dossierReconciler.Reconcile(ctx, targetConvID)
		if err != nil {
			return fmt.Errorf("reconcile dossier: %w", err)
		}
		if outcome != nil && outcome.Dossier != nil {
			fmt.Printf("[+] Living Dossier reconciled! Version: %d | Path: %s (%d chars)\n",
				outcome.Dossier.Version, outcome.MarkdownPath, len(outcome.Dossier.MarkdownContent))
		}
		return nil
	}

	// 3. Connect to Google Messages
	logger.Info().Msg("Connecting to Google Messages...")
	gmClient, err := gm.NewClient(gm.ClientOptions{
		SessionPath: sessionPath,
		Logger:      logger.With().Str("component", "libgm").Logger(),
	})
	if err != nil {
		return fmt.Errorf("failed to load Google Messages session: %w", err)
	}

	if !gmClient.LibGM().IsLoggedIn() {
		return fmt.Errorf("session at %s is not logged in / paired", sessionPath)
	}

	if err := gmClient.Connect(ctx); err != nil {
		return fmt.Errorf("failed to connect to Google Messages: %w", err)
	}
	time.Sleep(2 * time.Second)

	// 4. Resolve Target Conversation ID
	targetConvID := ingestConvID
	targetName := ingestContact

	if targetConvID == "" {
		logger.Info().Str("contact_name", ingestContact).Msg("Resolving conversation ID for contact...")
		resolved := resolveTrackedConversations(ctx, logger, gmClient, []string{ingestContact})
		if len(resolved) > 0 {
			targetConvID = resolved[0]
		}
	}

	if targetConvID == "" {
		return fmt.Errorf("could not resolve conversation ID for %q", ingestContact)
	}

	fmt.Println("\n=======================================================")
	fmt.Printf("  Target Conversation ID: %s (%s)\n", targetConvID, targetName)
	fmt.Printf("  Ingestion Batch Limit:  %d messages\n", ingestLimit)
	fmt.Println("=======================================================")

	// Ensure conversation exists in DB
	if err := database.UpsertConversation(ctx, &db.Conversation{
		ID:        targetConvID,
		Name:      targetName,
		IsTracked: true,
	}); err != nil {
		return fmt.Errorf("failed to upsert conversation: %w", err)
	}

	// 5. Run ETL Batch
	logger.Info().Str("conversation_id", targetConvID).Int64("limit", ingestLimit).Msg("Extracting and ingesting messages...")

	mediaDir := "./data/media"
	_ = os.MkdirAll(mediaDir, 0755)

	mediaIngestor := &pipeline.MediaIngestor{
		MediaDir:          mediaDir,
		Database:          database,
		Downloader:        gmClient,
		PullFullSizeMedia: false,
		Logger:            logger.With().Str("component", "media").Logger(),
	}

	etl := &pipeline.ETLPipeline{
		Fetcher:  gmClient,
		Database: database,
		Media:    mediaIngestor,
	}

	var totalInserted int
	var cursor []byte

	const pageSize int64 = 100
	for {
		batchSize := pageSize
		if ingestLimit > 0 {
			remaining := ingestLimit - int64(totalInserted)
			if remaining <= 0 {
				break
			}
			if remaining < batchSize {
				batchSize = remaining
			}
		}

		nextCursor, inserted, err := etl.RunBatch(ctx, targetConvID, batchSize, cursor)
		if err != nil {
			return fmt.Errorf("ETL run batch failed: %w", err)
		}
		totalInserted += inserted
		logger.Info().Int("inserted_in_batch", inserted).Int("total_inserted", totalInserted).Str("next_cursor", string(nextCursor)).Msg("Batch inserted")

		if inserted == 0 || len(nextCursor) == 0 || string(nextCursor) == string(cursor) {
			break
		}
		cursor = nextCursor
		time.Sleep(250 * time.Millisecond)
	}

	fmt.Printf("\n[+] Successfully ingested %d messages into PostgreSQL!\n", totalInserted)

	// 6. Sessionize & Living Dossier
	if !ingestSkipLLM {
		logger.Info().Msg("Initializing LLM client for sessionization and Living Dossier...")
		cfg, _ := config.LoadConfig()

		llmBase := cfg.LLMBaseURL
		if strings.Contains(llmBase, "litellm:4000") {
			// On host, reach litellm on mapped port 8448
			llmBase = "http://localhost:8448"
		}
		embedBase := cfg.EmbeddingBaseURL
		if strings.Contains(embedBase, "litellm:4000") {
			embedBase = "http://localhost:8448"
		}

		llmClient := pipeline.NewOpenAIClient(pipeline.LLMClientConfig{
			LLMBaseURL:          llmBase,
			LLMAPIKey:           cfg.LLMAPIKey,
			SummaryModel:        cfg.SummaryModel,
			EmbeddingBaseURL:    embedBase,
			EmbeddingAPIKey:     cfg.EmbeddingAPIKey,
			EmbeddingModel:      cfg.EmbeddingModel,
			EmbeddingDimensions: cfg.EmbeddingDimensions,
		})

		sessionizer := pipeline.NewSessionizer(pipeline.SessionizerConfig{
			Store:           database,
			LLMClient:       llmClient,
			SessionGapHours: cfg.SessionGapHours,
			Logger:          logger.With().Str("component", "sessionizer").Logger(),
		})

		logger.Info().Str("conversation_id", targetConvID).Msg("Sessionizing newly ingested messages...")
		sessions, err := sessionizer.ProcessUnassignedMessages(ctx, targetConvID)
		if err != nil {
			logger.Warn().Err(err).Msg("Sessionizer processing failed")
		} else {
			fmt.Printf("[+] Created/Updated %d conversation sessions with vector embeddings!\n", len(sessions))
			for idx, s := range sessions {
				sumText := "No summary"
				if s.Summary != nil {
					sumText = *s.Summary
				}
				fmt.Printf("    Session %d: %s (%s to %s) | %d messages\n",
					idx+1, sumText, s.StartedAt.Format("2006-01-02 15:04"), s.EndedAt.Format("15:04"), s.MessageCount)
			}
		}

		// Reconcile Living Dossier
		dossierDir := "./data/dossiers"
		_ = os.MkdirAll(dossierDir, 0755)

		dossierModel := pipeline.NewOpenAIDossierModel(pipeline.DossierModelConfig{
			BaseURL: llmBase,
			APIKey:  cfg.LLMAPIKey,
			Model:   cfg.DossierModel,
			Logger:  logger.With().Str("component", "dossier_model").Logger(),
		})

		dossierReconciler := pipeline.NewDossierReconciler(pipeline.DossierReconcilerConfig{
			Store:     database,
			Model:     dossierModel,
			OutputDir: dossierDir,
			Logger:    logger.With().Str("component", "dossier").Logger(),
		})

		logger.Info().Str("conversation_id", targetConvID).Msg("Reconciling Living Dossier...")
		outcome, err := dossierReconciler.Reconcile(ctx, targetConvID)
		if err != nil {
			logger.Warn().Err(err).Msg("Dossier reconciliation failed")
		} else if outcome != nil && outcome.Dossier != nil {
			fmt.Printf("\n=======================================================\n")
			fmt.Printf("  Living Dossier Reconciled (v%d) for %s:\n", outcome.Dossier.Version, outcome.Dossier.ContactName)
			fmt.Println("=======================================================")
			fmt.Println(outcome.Dossier.MarkdownContent)
		}
	}

	fmt.Println("\n=======================================================")
	fmt.Println("  Ingestion & Pipeline Run Complete!")
	fmt.Println("=======================================================")

	return nil
}
