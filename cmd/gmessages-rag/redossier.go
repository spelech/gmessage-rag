package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"

	"github.com/spelech/gmessage-rag/internal/config"
	"github.com/spelech/gmessage-rag/internal/db"
	"github.com/spelech/gmessage-rag/internal/notify"
	"github.com/spelech/gmessage-rag/internal/pipeline"
)

var redossierCmd = &cobra.Command{
	Use:   "redossier",
	Short: "Re-reconcile a Living Dossier from scratch or from a start date",
	Long: `Re-reconciles a contact's Living Dossier from scratch across all historical
sessions, or from an optional --since start date. Eliminates duplicate sections,
re-anchors open loops with antecedent hopping, and supports custom output formats
(markdown, slack, card, json).`,
	RunE: runRedossier,
}

var (
	redossierConv   string
	redossierSince  string
	redossierFormat string
	redossierDryRun bool
	redossierDBURL  string
)

func init() {
	redossierCmd.Flags().StringVar(&redossierConv, "conv", "", "Conversation ID or contact name to re-reconcile (required)")
	redossierCmd.Flags().StringVar(&redossierSince, "since", "", "Optional ISO-8601 start date (YYYY-MM-DD or RFC3339) to anchor the rebuild. If omitted, all sessions are processed.")
	redossierCmd.Flags().StringVar(&redossierFormat, "format", "markdown", "Output format: markdown, slack, card, or json")
	redossierCmd.Flags().BoolVar(&redossierDryRun, "dry-run", false, "Preview the re-reconciliation without overwriting the active dossier in database")
	redossierCmd.Flags().StringVar(&redossierDBURL, "db", "", "PostgreSQL database URL")
	rootCmd.AddCommand(redossierCmd)
}

func runRedossier(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	logger := log.With().Str("component", "redossier").Logger()

	targetConv := strings.TrimSpace(redossierConv)
	if targetConv == "" {
		return fmt.Errorf("provide --conv <id>")
	}

	var sinceTime *time.Time
	if strings.TrimSpace(redossierSince) != "" {
		parsed, err := time.Parse("2006-01-02", strings.TrimSpace(redossierSince))
		if err != nil {
			parsed, err = time.Parse(time.RFC3339, strings.TrimSpace(redossierSince))
		}
		if err != nil {
			return fmt.Errorf("invalid --since date %q: %w", redossierSince, err)
		}
		utc := parsed.UTC()
		sinceTime = &utc
	}

	dbURL := redossierDBURL
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

	// Resolve models
	base := config.ModelSettings{
		SummaryModel:   cfg.SummaryModel,
		DossierModel:   cfg.DossierModel,
		VisionModel:    cfg.VisionModel,
		EmbeddingModel: cfg.EmbeddingModel,
	}
	models := config.NewModelRegistry(base)
	if stored, err := database.GetSettings(ctx); err == nil {
		_ = models.Set(config.ModelSettingsFromMap(base, stored))
	}

	llmBase := resolveLLMEndpoint(cfg.LLMBaseURL)
	dossierModel := pipeline.NewOpenAIDossierModel(pipeline.DossierModelConfig{
		BaseURL:    llmBase,
		APIKey:     cfg.LLMAPIKey,
		Model:      cfg.DossierModel,
		ModelFunc:  models.DossierModel,
		HTTPClient: &http.Client{Timeout: 10 * time.Minute},
		Logger:     logger.With().Str("component", "dossier_model").Logger(),
	})

	// Pre-populate deterministic reply links for this conversation
	if n, err := database.PopulateDeterministicReplyLinks(ctx, targetConv); err == nil && n > 0 {
		logger.Info().Int64("links_created", n).Msg("Populated deterministic reply links")
	}

	// Fetch sessions
	sessions, err := database.GetSessionsSince(ctx, targetConv, sinceTime)
	if err != nil {
		return fmt.Errorf("get sessions: %w", err)
	}
	if len(sessions) == 0 {
		logger.Warn().Str("conversation_id", targetConv).Msg("No sessions found to re-reconcile")
		return nil
	}

	// Run OpenLoopLinker across sessions to ensure message_links graph is fresh
	linker := pipeline.NewOpenLoopLinker(database, nil, logger.With().Str("component", "open_loop_linker").Logger())
	for i := range sessions {
		_, _ = linker.DetectAndRecordLinks(ctx, targetConv, &sessions[i])
	}

	conv, err := database.GetConversation(ctx, targetConv)
	contactName := targetConv
	var participants []string
	if err == nil && conv != nil {
		if conv.Name != "" {
			contactName = conv.Name
		}
		participants = conv.Participants
	}

	// Load prior dossier for context, stripping duplicate sections
	prior, _ := database.GetContactDossier(ctx, targetConv)
	var priorMD string
	var priorSt map[string]any
	if prior != nil && !redossierDryRun {
		priorMD = pipeline.StripRenderedDossierSections(prior.MarkdownContent)
		priorSt = prior.StructuredState
	}

	latest := sessions[len(sessions)-1]
	anchor := latest.EndedAt.UTC()

	var sessionIDs []int64
	for _, s := range sessions {
		sessionIDs = append(sessionIDs, s.ID)
	}
	var antecedentDigest string
	if ants, err := database.GetAntecedentSessions(ctx, sessionIDs); err == nil && len(ants) > 0 {
		var b strings.Builder
		for _, ant := range ants {
			fmt.Fprintf(&b, "=== Antecedent Session %d [%s] ===\n", ant.ID, ant.StartedAt.Format("2006-01-02 15:04"))
			if ant.Summary != nil {
				fmt.Fprintf(&b, "Summary: %s\n", *ant.Summary)
			}
			if ant.Transcript != "" {
				fmt.Fprintf(&b, "Transcript:\n%s\n", ant.Transcript)
			}
			b.WriteString("\n")
		}
		antecedentDigest = strings.TrimSpace(b.String())
	}

	logger.Info().
		Str("conversation_id", targetConv).
		Int("session_count", len(sessions)).
		Msg("Invoking model reconciliation...")

	formatDigest := func(ss []db.ConversationSession) string {
		var b strings.Builder
		for _, s := range ss {
			fmt.Fprintf(&b, "=== Session %d [%s] ===\n", s.ID, s.StartedAt.Format("2006-01-02 15:04"))
			if s.Summary != nil {
				fmt.Fprintf(&b, "Summary: %s\n", *s.Summary)
			}
			if s.Transcript != "" {
				fmt.Fprintf(&b, "Transcript:\n%s\n", s.Transcript)
			}
			b.WriteString("\n")
		}
		return strings.TrimSpace(b.String())
	}

	res, err := dossierModel.Reconcile(ctx, pipeline.DossierReconcileRequest{
		ContactName:      contactName,
		PriorMarkdown:    priorMD,
		PriorState:       priorSt,
		SessionDigest:    formatDigest(sessions),
		AntecedentDigest: antecedentDigest,
		AnchorDate:       anchor,
	})
	if err != nil {
		return fmt.Errorf("reconciliation failed: %w", err)
	}

	state := res.StructuredState
	if state == nil {
		state = make(map[string]any)
	}

	pipeline.AnchorRelativeDates(state, anchor)
	pipeline.ApplyChronologicalOverrides(state, res.Supersedes)
	pipeline.TransitionPastEvents(state, anchor)

	// Enrich with contacts using the name safeguard
	state = pipeline.EnrichStructuredStateWithContacts(state, nil, participants, contactName)

	renderedMD := pipeline.RenderDossierMarkdown(contactName, state, res.Markdown)

	triggerSessionID := latest.ID
	lastProcessed := latest.EndedAt.UTC()

	var finalDossier *db.ContactDossier
	if !redossierDryRun {
		saved, err := database.UpsertContactDossier(
			ctx, targetConv, contactName, renderedMD, state, &triggerSessionID, &lastProcessed,
		)
		if err != nil {
			return fmt.Errorf("persist rebuilt dossier: %w", err)
		}
		finalDossier = saved
		logger.Info().
			Str("conversation_id", targetConv).
			Int("version", saved.Version).
			Msg("Rebuilt Living Dossier persisted to database")
	} else {
		finalDossier = &db.ContactDossier{
			ConversationID:         targetConv,
			ContactName:            contactName,
			MarkdownContent:        renderedMD,
			StructuredState:        state,
			LastProcessedMessageAt: &lastProcessed,
			Version:                0,
			UpdatedAt:              time.Now().UTC(),
		}
		logger.Info().Msg("[DRY RUN] Dossier rebuilt in memory only")
	}

	// Output in the requested format
	switch strings.ToLower(strings.TrimSpace(redossierFormat)) {
	case "slack":
		card := notify.SlackDossierCard(finalDossier)
		enc, _ := json.MarshalIndent(card, "", "  ")
		fmt.Println(string(enc))
	case "card":
		card := notify.AdaptiveDossierCard(finalDossier)
		enc, _ := json.MarshalIndent(card, "", "  ")
		fmt.Println(string(enc))
	case "json":
		enc, _ := json.MarshalIndent(finalDossier, "", "  ")
		fmt.Println(string(enc))
	default:
		fmt.Println(finalDossier.MarkdownContent)
	}

	return nil
}
