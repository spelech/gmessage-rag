package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"

	"github.com/spelech/gmessage-rag/internal/config"
	"github.com/spelech/gmessage-rag/internal/db"
	"github.com/spelech/gmessage-rag/internal/pipeline"
)

var resummarizeCmd = &cobra.Command{
	Use:   "resummarize",
	Short: "Regenerate missing session summaries and embeddings",
	Long: `Regenerates summaries and embeddings for existing conversation sessions that
lack them, without changing session boundaries or message linkage. Use this
after a model or endpoint outage left sessions stored without summaries.`,
	RunE: runResummarize,
}

var (
	resummarizeConv    string
	resummarizeAll     bool
	resummarizeLimit   int
	resummarizeWorkers int
	resummarizeDBURL   string
)

func init() {
	resummarizeCmd.Flags().StringVar(&resummarizeConv, "conv", "", "Conversation ID to re-summarize (omit with --all)")
	resummarizeCmd.Flags().BoolVar(&resummarizeAll, "all", false, "Re-summarize every conversation")
	resummarizeCmd.Flags().IntVar(&resummarizeLimit, "limit", 5000, "Maximum number of sessions to process")
	resummarizeCmd.Flags().IntVar(&resummarizeWorkers, "workers", 4, "Number of sessions to process concurrently")
	resummarizeCmd.Flags().StringVar(&resummarizeDBURL, "db", "", "PostgreSQL database URL")
	rootCmd.AddCommand(resummarizeCmd)
}

func runResummarize(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	logger := log.With().Str("component", "resummarize").Logger()

	targetConv := strings.TrimSpace(resummarizeConv)
	if !resummarizeAll && targetConv == "" {
		return fmt.Errorf("provide --conv <id> or --all")
	}
	if resummarizeAll {
		targetConv = ""
	}

	// Resolve database URL, mirroring the ingest command's host/container handling.
	dbURL := resummarizeDBURL
	if dbURL == "" {
		dbURL = os.Getenv("DATABASE_URL")
	}
	if dbURL == "" {
		dbURL = "postgres://rcsrag:changeme@127.0.0.1:5432/gmessages?sslmode=disable"
	} else {
		dbURL = resolveDatabaseURL(dbURL)
	}

	logger.Info().Str("db_url", dbURL).Msg("Connecting to PostgreSQL database...")
	database, err := db.New(ctx, dbURL)
	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}
	defer database.Close()

	if err := database.Migrate(ctx); err != nil {
		return fmt.Errorf("failed to apply migrations: %w", err)
	}

	cfg, err := config.LoadConfig()
	if err != nil {
		return fmt.Errorf("failed to load configuration: %w", err)
	}

	// Seed the model registry from stored settings over environment defaults so
	// re-summarization honours the models chosen in the dashboard.
	base := config.ModelSettings{
		SummaryModel:   cfg.SummaryModel,
		DossierModel:   cfg.DossierModel,
		VisionModel:    cfg.VisionModel,
		EmbeddingModel: cfg.EmbeddingModel,
	}
	models := config.NewModelRegistry(base)
	if stored, err := database.GetSettings(ctx); err != nil {
		logger.Warn().Err(err).Msg("Could not load persisted model settings; using environment defaults")
	} else if err := models.Set(config.ModelSettingsFromMap(base, stored)); err != nil {
		logger.Warn().Err(err).Msg("Ignoring invalid persisted model settings; using environment defaults")
	}

	llmBase := resolveLLMEndpoint(cfg.LLMBaseURL)
	embedBase := resolveLLMEndpoint(cfg.EmbeddingBaseURL)

	llmClient := pipeline.NewOpenAIClient(pipeline.LLMClientConfig{
		LLMBaseURL:          llmBase,
		LLMAPIKey:           cfg.LLMAPIKey,
		SummaryModel:        cfg.SummaryModel,
		SummaryModelFunc:    models.SummaryModel,
		EmbeddingBaseURL:    embedBase,
		EmbeddingAPIKey:     cfg.EmbeddingAPIKey,
		EmbeddingModel:      cfg.EmbeddingModel,
		EmbeddingModelFunc:  models.EmbeddingModel,
		EmbeddingDimensions: cfg.EmbeddingDimensions,
	})

	sessionizer := pipeline.NewSessionizer(pipeline.SessionizerConfig{
		Store:           database,
		LLMClient:       llmClient,
		SessionGapHours: cfg.SessionGapHours,
		Location:        cfg.Location,
		Logger:          logger.With().Str("component", "sessionizer").Logger(),
		Workers:         resummarizeWorkers,
	})

	if targetConv == "" {
		logger.Info().Msg("Re-summarizing sessions missing summaries/embeddings across all conversations")
	} else {
		logger.Info().Str("conversation_id", targetConv).Msg("Re-summarizing sessions missing summaries/embeddings")
	}

	report, err := sessionizer.ResummarizeMissing(ctx, targetConv, resummarizeLimit)
	if err != nil {
		return fmt.Errorf("resummarize: %w", err)
	}

	printResummarizeReport(logger, report)
	return nil
}

func printResummarizeReport(logger zerolog.Logger, report *pipeline.ResummarizeReport) {
	if report == nil {
		return
	}
	fmt.Printf("\n[+] Re-summarization complete\n")
	fmt.Printf("    Requested: %d\n", report.Requested)
	fmt.Printf("    Updated:   %d\n", report.Updated)
	fmt.Printf("    Skipped:   %d\n", report.Skipped)
	fmt.Printf("    Failed:    %d\n", report.Failed)
	for _, e := range report.Errors {
		fmt.Printf("    - %s\n", e)
	}
	logger.Info().
		Int("requested", report.Requested).
		Int("updated", report.Updated).
		Int("skipped", report.Skipped).
		Int("failed", report.Failed).
		Msg("Re-summarization pass complete")
}
