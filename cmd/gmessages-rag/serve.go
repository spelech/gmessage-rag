package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
	"unicode"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"

	"github.com/spelech/gmessage-rag/internal/config"
	"github.com/spelech/gmessage-rag/internal/db"
	"github.com/spelech/gmessage-rag/internal/gm"
	"github.com/spelech/gmessage-rag/internal/mcpserver"
	"github.com/spelech/gmessage-rag/internal/notify"
	"github.com/spelech/gmessage-rag/internal/obs"
	"github.com/spelech/gmessage-rag/internal/pipeline"
	"github.com/spelech/gmessage-rag/internal/web"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
)

// DaemonServer bundles runtime components for clean lifecycle management.
type DaemonServer struct {
	Config     *config.Config
	DB         *db.DB
	GMClient   *gm.Client
	Watchdog   *notify.Watchdog
	Notify     *notify.Client
	Debouncer  *pipeline.SmartDebouncer
	Metrics    *obs.Metrics
	MCPServer  *mcpserver.Server
	Tuner      *config.RuntimeTuner
	HTTPServer *http.Server
	RootMux    *http.ServeMux
	Logger     zerolog.Logger
}

// BuildServeMux constructs the root http.ServeMux with all routes registered.
func BuildServeMux(database *db.DB, gmClient *gm.Client, mcpSrv *mcpserver.Server, metrics *obs.Metrics) *http.ServeMux {
	return BuildServeMuxWithTuner(database, gmClient, mcpSrv, metrics, nil)
}

// BuildServeMuxWithTuner constructs the root http.ServeMux with optional RuntimeTuner wired in.
func BuildServeMuxWithTuner(database *db.DB, gmClient *gm.Client, mcpSrv *mcpserver.Server, metrics *obs.Metrics, tuner *config.RuntimeTuner) *http.ServeMux {
	rootMux := http.NewServeMux()

	// 1. Prometheus metrics: /metrics
	if metrics != nil {
		rootMux.Handle("/metrics", metrics.Handler())
	} else {
		rootMux.Handle("/metrics", obs.NewMetricsRegistry().Handler())
	}

	// 2. Health probe: /healthz
	rootMux.Handle("/healthz", obs.NewHealthHandler(database))

	// 3. MCP agent server: /mcp and /sse
	if mcpSrv != nil {
		rootMux.Handle("/mcp", mcpSrv)
		rootMux.HandleFunc("/sse", mcpSrv.ServeSSE)
	}

	// 4. Diagnostic Test Bench: /api/v1/testbench/*
	tbHandler := web.NewTestBench(mcpSrv, database)
	tbHandler.RegisterRoutes(rootMux)

	// 5. Web UI Dashboard and API: /, /dashboard, /pair, /api/v1/*
	dashHandler := web.NewDashboardHandlerWithTuner(database, gmClient, tuner)
	rootMux.Handle("/", dashHandler)

	return rootMux
}

func cleanDigits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// resolveTrackedConversations converts human names, phone numbers, or conversation IDs
// into concrete Google Messages conversation IDs by querying active conversations.
func resolveTrackedConversations(ctx context.Context, logger zerolog.Logger, gmClient *gm.Client, targets []string) []string {
	if len(targets) == 0 || gmClient == nil {
		return nil
	}

	convResp, err := gmClient.ListConversations(ctx, 100)
	if err != nil {
		logger.Warn().Err(err).Msg("Could not list conversations to resolve tracked contacts; falling back to raw targets")
		return targets
	}

	convs := convResp.GetConversations()
	var resolved []string
	seen := make(map[string]bool)

	for _, target := range targets {
		target = strings.TrimSpace(target)
		if target == "" {
			continue
		}

		matchedID := ""
		for _, c := range convs {
			if c == nil {
				continue
			}
			convID := c.GetConversationID()
			if convID == target {
				matchedID = convID
				break
			}
			if c.GetName() != "" && strings.EqualFold(strings.TrimSpace(c.GetName()), target) {
				matchedID = convID
				break
			}
			for _, p := range c.GetParticipants() {
				if p == nil {
					continue
				}
				if p.GetFullName() != "" && strings.EqualFold(strings.TrimSpace(p.GetFullName()), target) {
					matchedID = convID
					break
				}
				cleanP := cleanDigits(p.GetFormattedNumber())
				cleanT := cleanDigits(target)
				if cleanT != "" && (cleanP == cleanT || strings.HasSuffix(cleanP, cleanT) || strings.HasSuffix(cleanT, cleanP)) {
					matchedID = convID
					break
				}
			}
			if matchedID != "" {
				break
			}
		}

		finalID := target
		if matchedID != "" {
			logger.Info().Str("target", target).Str("conversation_id", matchedID).Msg("Resolved tracked contact to conversation ID")
			finalID = matchedID
		} else {
			logger.Warn().Str("target", target).Msg("No matching conversation found for tracked contact; using raw identifier")
		}

		if !seen[finalID] {
			seen[finalID] = true
			resolved = append(resolved, finalID)
		}
	}

	return resolved
}

// StartETLForTrackedContacts triggers background ETL backfill ingestion for tracked contacts and
// sessionizes the newly ingested messages.
func StartETLForTrackedContacts(ctx context.Context, logger zerolog.Logger, database *db.DB, gmClient *gm.Client, sessionizer *pipeline.Sessionizer, media *pipeline.MediaIngestor, trackedContacts []string, batchSize int, onContactSynced ...func(context.Context, string)) {
	if len(trackedContacts) == 0 || database == nil || gmClient == nil {
		return
	}

	go func() {
		// Wait briefly for initial Google Messages session connection if logged in
		for i := 0; i < 20; i++ {
			if gmClient.LibGM() == nil || gmClient.LibGM().IsConnected() || !gmClient.LibGM().IsLoggedIn() {
				break
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(250 * time.Millisecond):
			}
		}

		resolvedContacts := resolveTrackedConversations(ctx, logger, gmClient, trackedContacts)
		logger.Info().Int("count", len(resolvedContacts)).Msg("Starting background ETL sync for tracked contacts")
		etl := &pipeline.ETLPipeline{
			Fetcher:  gmClient,
			Database: database,
			Media:    media,
		}

		maxMessages := int64(batchSize)
		if maxMessages <= 0 {
			maxMessages = 100
		}
		const pageSize int64 = 100

		for idx, contactID := range resolvedContacts {
			select {
			case <-ctx.Done():
				logger.Info().Msg("Background ETL aborted due to shutdown")
				return
			default:
			}

			contactName := contactID
			if idx < len(trackedContacts) && strings.TrimSpace(trackedContacts[idx]) != "" {
				contactName = strings.TrimSpace(trackedContacts[idx])
			}
			_ = database.UpsertConversation(ctx, &db.Conversation{
				ID:        contactID,
				Name:      contactName,
				IsTracked: true,
			})

			logger.Debug().Str("contact_id", contactID).Msg("Processing ETL backfill for contact")
			var cursor []byte
			var totalFetched int64
			for {
				select {
				case <-ctx.Done():
					return
				default:
				}

				remaining := maxMessages - totalFetched
				if remaining <= 0 {
					break
				}
				fetchCount := pageSize
				if remaining < fetchCount {
					fetchCount = remaining
				}

				nextCursor, inserted, err := etl.RunBatch(ctx, contactID, fetchCount, cursor)
				if err != nil {
					logger.Warn().Err(err).Str("contact_id", contactID).Msg("ETL backfill batch failed for contact")
					break
				}
				totalFetched += int64(inserted)

				if inserted == 0 || len(nextCursor) == 0 || string(nextCursor) == string(cursor) {
					break
				}
				cursor = nextCursor

				// Pace ingestion between batches to prevent CPU/IO spikes
				select {
				case <-ctx.Done():
					return
				case <-time.After(250 * time.Millisecond):
				}
			}

			if sessionizer != nil {
				if _, err := sessionizer.ProcessUnassignedMessages(ctx, contactID); err != nil && ctx.Err() == nil {
					logger.Warn().Err(err).Str("contact_id", contactID).Msg("Sessionizer processing failed after ETL backfill")
				}
			}

			for _, fn := range onContactSynced {
				if fn != nil && ctx.Err() == nil {
					fn(ctx, contactID)
				}
			}
		}
		logger.Info().Msg("Completed background ETL sync pass for tracked contacts")
	}()
}

// SyncTrackedContacts performs one forward, incremental sync pass over the tracked
// conversations while a connection is open. Unlike StartETLForTrackedContacts it
// pages from the newest message and stops at the first page with no new rows, so
// it stays cheap to run every periodic sync window. It is synchronous; callers
// run it inside a bounded window.
func SyncTrackedContacts(ctx context.Context, logger zerolog.Logger, database *db.DB, gmClient *gm.Client, sessionizer *pipeline.Sessionizer, media *pipeline.MediaIngestor, trackedContacts []string, batchSize int, onContactSynced ...func(context.Context, string)) {
	if len(trackedContacts) == 0 || database == nil || gmClient == nil {
		return
	}

	resolvedContacts := resolveTrackedConversations(ctx, logger, gmClient, trackedContacts)
	logger.Info().Int("count", len(resolvedContacts)).Msg("Periodic sync: draining tracked conversations")

	etl := &pipeline.ETLPipeline{
		Fetcher:  gmClient,
		Database: database,
		Media:    media,
	}

	pageSize := int64(batchSize)
	if pageSize <= 0 {
		pageSize = 100
	}

	for idx, contactID := range resolvedContacts {
		select {
		case <-ctx.Done():
			logger.Info().Msg("Periodic sync aborted due to shutdown")
			return
		default:
		}

		contactName := contactID
		if idx < len(trackedContacts) && strings.TrimSpace(trackedContacts[idx]) != "" {
			contactName = strings.TrimSpace(trackedContacts[idx])
		}
		_ = database.UpsertConversation(ctx, &db.Conversation{
			ID:        contactID,
			Name:      contactName,
			IsTracked: true,
		})

		var cursor []byte
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			nextCursor, inserted, err := etl.SyncBatch(ctx, contactID, pageSize, cursor)
			if err != nil {
				logger.Warn().Err(err).Str("contact_id", contactID).Msg("Periodic sync batch failed")
				break
			}
			if inserted == 0 || len(nextCursor) == 0 || string(nextCursor) == string(cursor) {
				break
			}
			cursor = nextCursor
		}

		if sessionizer != nil {
			if _, err := sessionizer.ProcessUnassignedMessages(ctx, contactID); err != nil && ctx.Err() == nil {
				logger.Warn().Err(err).Str("contact_id", contactID).Msg("Sessionizer processing failed after periodic sync")
			}
		}

		for _, fn := range onContactSynced {
			if fn != nil && ctx.Err() == nil {
				fn(ctx, contactID)
			}
		}
	}
	logger.Info().Msg("Periodic sync: drain complete")
}

// dispatchDossierWebhook delivers a dossier update event with a bounded timeout. Failures are
// logged but never abort the flush, because the dossier is already persisted.
// Requirement ID: TRANS-03
func dispatchDossierWebhook(ctx context.Context, logger zerolog.Logger, dispatcher *notify.WebhookDispatcher, dossier *db.ContactDossier) {
	if dispatcher == nil || !dispatcher.Enabled() || dossier == nil {
		return
	}

	webhookCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	event := notify.DossierWebhookEvent{
		Event:           "dossier.updated",
		ConversationID:  dossier.ConversationID,
		ContactName:     dossier.ContactName,
		Version:         dossier.Version,
		ChangedAt:       dossier.UpdatedAt,
		StructuredState: dossier.StructuredState,
		Markdown:        dossier.MarkdownContent,
	}

	if err := dispatcher.DispatchDossierUpdate(webhookCtx, event); err != nil {
		logger.Warn().Err(err).Str("conversation_id", dossier.ConversationID).Msg("Dossier webhook dispatch failed")
		return
	}
	logger.Info().Str("conversation_id", dossier.ConversationID).Int("version", dossier.Version).Msg("Dispatched dossier webhook")
}

func runServe(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer cancel()

	logger := log.With().Str("component", "daemon").Logger()
	logger.Info().Msg("Initializing gmessages-rag ingestion daemon (DAEMON-01)")

	// 1. Parse configuration
	cfg, err := config.LoadConfig()
	if err != nil {
		return fmt.Errorf("failed to load configuration: %w", err)
	}

	// 2. Initialize database connection pool
	database, err := db.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("failed to connect to database at %s: %w", cfg.DatabaseURL, err)
	}
	defer database.Close()

	// 3. Run embedded database migrations
	logger.Info().Msg("Applying embedded database migrations...")
	if err := database.Migrate(ctx); err != nil {
		return fmt.Errorf("failed to apply migrations: %w", err)
	}

	// 4. Initialize notification and watchdog monitoring
	notifyClient := notify.NewClient(notify.ClientConfig{
		AppriseURL:     cfg.AppriseURL,
		NtfyURL:        cfg.NtfyURL,
		ExternalWebURL: cfg.ExternalWebURL,
	})

	watchdog := notify.NewWatchdog(notify.WatchdogConfig{
		NotifyClient:            notifyClient,
		NotifyOnPhoneDisconnect: cfg.NotifyOnPhoneDisconnect,
		GracePeriod:             time.Duration(cfg.PhoneDisconnectGraceMinutes) * time.Minute,
		Logger:                  logger.With().Str("component", "watchdog").Logger(),
	})

	// Prometheus metrics registry, wired by the callbacks below. Must be
	// declared before the OnFlush and OnMessageUpsert closures (those closures
	// capture by reference through the function scope).
	metrics := obs.NewMetricsRegistry()

	webhookDispatcher := notify.NewWebhookDispatcher(notify.WebhookConfig{
		URL:         cfg.WebhookURL,
		BearerToken: cfg.WebhookAuthToken,
		HMACSecret:  cfg.WebhookHMACSecret,
	})

	// 5. Initialize Vision Service, LLM Client, Sessionizer & Debouncer
	visionClient := pipeline.NewOpenAIVisionClient(pipeline.OpenAIVisionClientConfig{
		BaseURL: cfg.LLMBaseURL,
		APIKey:  cfg.LLMAPIKey,
		Model:   cfg.VisionModel,
		Logger:  logger.With().Str("component", "vision").Logger(),
	})
	visionService := &pipeline.VisionService{
		Client:   visionClient,
		Database: database,
		MediaDir: cfg.MediaDir,
		Logger:   logger.With().Str("component", "vision").Logger(),
	}

	llmClient := pipeline.NewOpenAIClient(pipeline.LLMClientConfig{
		LLMBaseURL:          cfg.LLMBaseURL,
		LLMAPIKey:           cfg.LLMAPIKey,
		SummaryModel:        cfg.SummaryModel,
		EmbeddingBaseURL:    cfg.EmbeddingBaseURL,
		EmbeddingAPIKey:     cfg.EmbeddingAPIKey,
		EmbeddingModel:      cfg.EmbeddingModel,
		EmbeddingDimensions: cfg.EmbeddingDimensions,
	})

	sessionizer := pipeline.NewSessionizer(pipeline.SessionizerConfig{
		Store:           database,
		LLMClient:       llmClient,
		SessionGapHours: cfg.SessionGapHours,
		Location:        cfg.Location,
		Logger:          logger.With().Str("component", "sessionizer").Logger(),
	})

	dossierModel := pipeline.NewOpenAIDossierModel(pipeline.DossierModelConfig{
		BaseURL: cfg.LLMBaseURL,
		APIKey:  cfg.LLMAPIKey,
		Model:   cfg.DossierModel,
		Logger:  logger.With().Str("component", "dossier_model").Logger(),
	})

	// gmClient is assigned after the debouncer is created; the contact source closes over it so
	// late-bound contacts fetching does not reorder daemon construction.
	var gmClient *gm.Client

	// Persist decrypted RCS media during ETL ingestion so the Vision service can
	// OCR stored image attachments on the next debounced flush (SEC-01). The
	// downloader is attached once gmClient exists below.
	mediaIngestor := &pipeline.MediaIngestor{
		Database:          database,
		MediaDir:          cfg.MediaDir,
		PullFullSizeMedia: cfg.PullFullSizeMedia,
		Logger:            logger.With().Str("component", "media").Logger(),
	}
	dossierReconciler := pipeline.NewDossierReconciler(pipeline.DossierReconcilerConfig{
		Store:     database,
		Model:     dossierModel,
		OutputDir: cfg.DossierDir,
		Location:  cfg.Location,
		Logger:    logger.With().Str("component", "dossier").Logger(),
		ContactSource: func(contactCtx context.Context) ([]gm.ContactSummary, error) {
			if gmClient == nil {
				return nil, nil
			}
			return gm.SyncContacts(contactCtx, gmClient)
		},
	})

	debouncer := pipeline.NewSmartDebouncer(pipeline.DebouncerConfig{
		IdleTimeout: time.Duration(cfg.DossierDebounceMinutes) * time.Minute,
		Logger:      logger.With().Str("component", "debouncer").Logger(),
		OnFlush: func(flushCtx context.Context, conversationID string) error {
			if cfg.PauseIngestion {
				logger.Debug().Str("conversation_id", conversationID).Msg("Ingestion paused: skipping debounced flush processing")
				return nil
			}
			logger.Info().Str("conversation_id", conversationID).Msg("Debounced conversation flush triggered")
			// Process pending vision OCR for image attachments in this conversation (PIPE-01)
			if count, ocrErr := visionService.ProcessUnprocessedAttachments(flushCtx, conversationID, 50); ocrErr != nil {
				logger.Warn().Err(ocrErr).Str("conversation_id", conversationID).Msg("Vision OCR processing encountered error")
			} else if count > 0 {
				logger.Info().Int("count", count).Str("conversation_id", conversationID).Msg("Processed vision OCR for attachments")
			}
			// Sessionize the newly ingested messages (PIPE-06 / DB-03)
			sessions, err := sessionizer.ProcessUnassignedMessages(flushCtx, conversationID)
			if err != nil {
				logger.Error().Err(err).Str("conversation_id", conversationID).Msg("Failed to sessionize messages on flush")
				return err
			}
			logger.Info().Str("conversation_id", conversationID).Int("session_count", len(sessions)).Msg("Completed sessionization on flush")
			metrics.SessionChunksCreatedTotal().Add(float64(len(sessions)))
			// Reconcile the Living Dossier from the freshly sessionized messages (PIPE-03 / PIPE-04)
			outcome, dossierErr := dossierReconciler.Reconcile(flushCtx, conversationID)
			if dossierErr != nil {
				logger.Error().Err(dossierErr).Str("conversation_id", conversationID).Msg("Failed to reconcile Living Dossier on flush")
				return dossierErr
			}
			if outcome != nil && outcome.Changed {
				logger.Info().Str("conversation_id", conversationID).Int("version", outcome.Dossier.Version).Msg("Reconciled Living Dossier on flush")
				dispatchDossierWebhook(flushCtx, logger, webhookDispatcher, outcome.Dossier)
			}
			return nil
		},
	})
	defer debouncer.Close()

	// 6. Initialize Google Messages client
	sessionPath := os.Getenv("SESSION_PATH")
	if sessionPath == "" {
		sessionPath = "/data/auth/session.json"
	}
	gmClient, err = gm.NewClient(gm.ClientOptions{
		SessionPath: sessionPath,
		Logger:      logger.With().Str("component", "libgm").Logger(),
		OnConversationActive: func(conversationID string) {
			debouncer.RecordEvent(conversationID, pipeline.EventInboundMessage)
		},
		OnTypingActivity: func(conversationID string, typingType gmproto.TypingTypes) {
			debouncer.RecordTypingType(conversationID, typingType)
		},
		// OnMessageUpsert persists real-time inbound messages. Without this
		// callback, handleIncomingMessage drops them silently and only the
		// historical backfill path ever writes to Postgres.
		OnMessageUpsert: func(upsertCtx context.Context, msg *gmproto.Message, isFromMe bool, _ time.Time) error {
			if msg == nil {
				return nil
			}
			if cfg.PauseIngestion {
				logger.Debug().Str("message_id", msg.GetMessageID()).Msg("Ingestion paused: skipping message persistence and media download")
				return nil
			}
			conversationID := msg.GetConversationID()
			dbMsg := gm.ProtoMessageToDBMessage(msg, conversationID, false)
			if dbMsg == nil {
				return nil
			}
			if upsertErr := database.UpsertMessage(upsertCtx, dbMsg); upsertErr != nil {
				logger.Error().Err(upsertErr).Str("conversation_id", conversationID).Str("message_id", dbMsg.ID).Msg("Failed to persist inbound message")
				return upsertErr
			}

			// Record Prometheus metric (OBS-01).
			direction := obs.DirectionInbound
			if isFromMe {
				direction = obs.DirectionOutbound
			}
			metrics.RecordMessageIngested(direction, obs.ProtocolRCS)

			// Download and persist any media attachments on this message (SEC-01).
			// The downloader on mediaIngestor is set before Connect, so it is
			// ready when real-time messages arrive.
			if mediaIngestor != nil && mediaIngestor.Downloader != nil {
				if _, mediaErr := mediaIngestor.IngestMessage(upsertCtx, msg); mediaErr != nil {
					logger.Warn().Err(mediaErr).Str("conversation_id", conversationID).Str("message_id", dbMsg.ID).Msg("Failed to download media for inbound message")
				}
			}
			return nil
		},
		OnPhoneNotResponding:   watchdog.OnPhoneNotResponding,
		OnPhoneRespondingAgain: watchdog.OnPhoneRespondingAgain,
		OnListenFatalError:     watchdog.OnListenFatalError,
	})
	if err != nil {
		return fmt.Errorf("failed to initialize Google Messages client: %w", err)
	}
	mediaIngestor.Downloader = gmClient

	// Apply connection tuning. A longer ping lowers wakeups; a lower alert
	// timeout keeps phone-not-responding detection inside the watchdog grace.
	gmClient.SetPingInterval(time.Duration(cfg.PingIntervalMinutes) * time.Minute)
	gmClient.SetAlertTimeoutCount(cfg.AlertTimeoutCount)

	// afterSync runs the shared post-ingestion work for one conversation.
	// Requirement ID: PIPE-01, PIPE-03
	afterSync := func(syncCtx context.Context, contactID string) {
		if cfg.PauseIngestion {
			return
		}
		if count, ocrErr := visionService.ProcessUnprocessedAttachments(syncCtx, contactID, 50); ocrErr != nil {
			logger.Warn().Err(ocrErr).Str("conversation_id", contactID).Msg("Vision OCR processing encountered error after ETL sync")
		} else if count > 0 {
			logger.Info().Int("count", count).Str("conversation_id", contactID).Msg("Processed vision OCR for attachments after ETL sync")
		}
		outcome, dossierErr := dossierReconciler.Reconcile(syncCtx, contactID)
		if dossierErr != nil {
			logger.Warn().Err(dossierErr).Str("conversation_id", contactID).Msg("Living Dossier reconciliation failed after ETL sync")
			return
		}
		if outcome != nil && outcome.Changed {
			logger.Info().Str("conversation_id", contactID).Int("version", outcome.Dossier.Version).Msg("Reconciled Living Dossier after ETL sync")
			dispatchDossierWebhook(syncCtx, logger, webhookDispatcher, outcome.Dossier)
		}
	}

	// Connect Google Messages client if credentials exist and are paired.
	// SYNC_MODE selects the connection supervisor: continuous (ReconnectLoop)
	// or periodic (connect for SYNC_WINDOW_MINUTES every SYNC_INTERVAL_MINUTES).
	// Requirement ID: SYNC-01
	if gmClient.LibGM().IsLoggedIn() {
		if cfg.SyncMode == "periodic" {
			logger.Info().
				Int("interval_minutes", cfg.SyncIntervalMinutes).
				Int("window_minutes", cfg.SyncWindowMinutes).
				Int("ping_interval_minutes", cfg.PingIntervalMinutes).
				Msg("Periodic sync mode enabled: connecting only during each sync window")
			go gmClient.PeriodicSyncLoop(ctx, gm.PeriodicSyncConfig{
				Interval: time.Duration(cfg.SyncIntervalMinutes) * time.Minute,
				Window:   time.Duration(cfg.SyncWindowMinutes) * time.Minute,
				Logger:   logger.With().Str("component", "sync").Logger(),
				OnWindow: func(windowCtx context.Context) {
					if cfg.PauseIngestion {
						return
					}
					drainCtx, cancel := context.WithTimeout(windowCtx, 10*time.Minute)
					defer cancel()
					SyncTrackedContacts(drainCtx, logger, database, gmClient, sessionizer, mediaIngestor, cfg.TrackedContacts, cfg.MaxBackfillMessages, afterSync)
				},
			})
		} else {
			logger.Info().Msg("Existing Google Messages session detected, initiating connect...")
			go gmClient.ReconnectLoop(ctx)
		}
	} else {
		logger.Warn().Msg("Google Messages session is not paired. Run 'pair' or navigate to /pair to authenticate.")
	}

	// 7. Initialize MCP Server, Runtime Tuner, and Root Mux
	mcpServer := mcpserver.NewServer(mcpserver.ServerOptions{
		Name:              "gmessages-rag",
		Version:           "0.1.0",
		EnableSendMessage: cfg.EnableMCPSendMessage,
		Database:          database,
		AccessToken:       cfg.MCPAccessToken,
	})

	tuner := config.NewRuntimeTuner(config.TunerSettings{
		DebouncerIdleMinutes: cfg.DossierDebounceMinutes,
		RRFKFactor:           config.DefaultRRFKFactor,
		OCREndpoint:          cfg.LLMBaseURL,
		BackfillBatchSize:    cfg.MaxBackfillMessages,
	})

	rootMux := BuildServeMuxWithTuner(database, gmClient, mcpServer, metrics, tuner)

	// 8. Start background batch ETL pipeline for any tracked contacts
	if cfg.PauseIngestion {
		logger.Info().Msg("PAUSE_INGESTION is enabled: background ETL backfill and real-time DB ingestion are disabled")
	} else {
		StartETLForTrackedContacts(ctx, logger, database, gmClient, sessionizer, mediaIngestor, cfg.TrackedContacts, cfg.MaxBackfillMessages, func(syncCtx context.Context, contactID string) {
			if count, ocrErr := visionService.ProcessUnprocessedAttachments(syncCtx, contactID, 50); ocrErr != nil {
				logger.Warn().Err(ocrErr).Str("conversation_id", contactID).Msg("Vision OCR processing encountered error after ETL sync")
			} else if count > 0 {
				logger.Info().Int("count", count).Str("conversation_id", contactID).Msg("Processed vision OCR for attachments after ETL sync")
			}
			outcome, dossierErr := dossierReconciler.Reconcile(syncCtx, contactID)
			if dossierErr != nil {
				logger.Warn().Err(dossierErr).Str("conversation_id", contactID).Msg("Living Dossier reconciliation failed after ETL sync")
				return
			}
			if outcome != nil && outcome.Changed {
				logger.Info().Str("conversation_id", contactID).Int("version", outcome.Dossier.Version).Msg("Reconciled Living Dossier after ETL sync")
				dispatchDossierWebhook(syncCtx, logger, webhookDispatcher, outcome.Dossier)
			}
		})
	}

	// 9. Configure and start HTTP server
	httpAddr := fmt.Sprintf(":%d", cfg.HTTPPort)
	srv := &http.Server{
		Addr:    httpAddr,
		Handler: rootMux,
		BaseContext: func(l net.Listener) context.Context {
			return ctx
		},
	}

	serverErrCh := make(chan error, 1)
	go func() {
		logger.Info().Str("addr", httpAddr).Msg("HTTP server listening")
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrCh <- err
		}
	}()

	// 10. Wait for OS signals or server fatal error
	select {
	case <-ctx.Done():
		logger.Info().Msg("Shutdown signal received, draining connections...")
	case srvErr := <-serverErrCh:
		logger.Error().Err(srvErr).Msg("HTTP server failed")
		return srvErr
	}

	// Graceful shutdown sequence
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error().Err(err).Msg("HTTP server graceful shutdown failed")
	}

	// Cancel any active watchdog grace period timers during orderly shutdown
	watchdog.OnPhoneRespondingAgain()

	logger.Info().Msg("Daemon stopped cleanly")
	return nil
}
