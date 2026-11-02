package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

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
	HTTPServer *http.Server
	RootMux    *http.ServeMux
	Logger     zerolog.Logger
}

// BuildServeMux constructs the root http.ServeMux with all routes registered.
func BuildServeMux(database *db.DB, gmClient *gm.Client, mcpSrv *mcpserver.Server, metrics *obs.Metrics) *http.ServeMux {
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
	dashHandler := web.NewDashboard(database, gmClient)
	rootMux.Handle("/", dashHandler)

	return rootMux
}

// StartETLForTrackedContacts triggers background ETL backfill ingestion for tracked contacts.
func StartETLForTrackedContacts(ctx context.Context, logger zerolog.Logger, database *db.DB, gmClient *gm.Client, trackedContacts []string, batchSize int) {
	if len(trackedContacts) == 0 || database == nil || gmClient == nil {
		return
	}

	go func() {
		logger.Info().Int("count", len(trackedContacts)).Msg("Starting background ETL sync for tracked contacts")
		etl := &pipeline.ETLPipeline{
			Fetcher:  gmClient,
			Database: database,
		}

		limit := int64(batchSize)
		if limit <= 0 {
			limit = 100
		}

		for _, contactID := range trackedContacts {
			select {
			case <-ctx.Done():
				logger.Info().Msg("Background ETL aborted due to shutdown")
				return
			default:
			}

			logger.Debug().Str("contact_id", contactID).Msg("Processing ETL backfill for contact")
			var cursor []byte
			for {
				select {
				case <-ctx.Done():
					return
				default:
				}

				nextCursor, inserted, err := etl.RunBatch(ctx, contactID, limit, cursor)
				if err != nil {
					logger.Warn().Err(err).Str("contact_id", contactID).Msg("ETL backfill batch failed for contact")
					break
				}

				if inserted == 0 || len(nextCursor) == 0 {
					break
				}
				cursor = nextCursor
			}
		}
		logger.Info().Msg("Completed background ETL sync pass for tracked contacts")
	}()
}

func runServe(cmd *cobra.Command, args []string) error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
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

	// 5. Initialize debouncer
	debouncer := pipeline.NewSmartDebouncer(pipeline.DebouncerConfig{
		IdleTimeout: time.Duration(cfg.DossierDebounceMinutes) * time.Minute,
		Logger:      logger.With().Str("component", "debouncer").Logger(),
		OnFlush: func(flushCtx context.Context, conversationID string) error {
			logger.Info().Str("conversation_id", conversationID).Msg("Debounced conversation flush triggered")
			return nil
		},
	})
	defer debouncer.Close()

	// 6. Initialize Google Messages client
	gmClient, err := gm.NewClient(gm.ClientOptions{
		SessionPath: "/data/auth/session.json",
		Logger:      logger.With().Str("component", "libgm").Logger(),
		OnConversationActive: func(conversationID string) {
			debouncer.RecordEvent(conversationID, pipeline.EventInboundMessage)
		},
	})
	if err != nil {
		return fmt.Errorf("failed to initialize Google Messages client: %w", err)
	}

	// Connect Google Messages client if credentials exist and are paired
	if gmClient.LibGM().IsLoggedIn() {
		logger.Info().Msg("Existing Google Messages session detected, initiating connect...")
		go gmClient.ReconnectLoop(ctx)
	} else {
		logger.Warn().Msg("Google Messages session is not paired. Run 'pair' or navigate to /pair to authenticate.")
	}

	// 7. Initialize Metrics, MCP Server, and Root Mux
	metrics := obs.NewMetricsRegistry()
	mcpServer := mcpserver.NewServer(mcpserver.ServerOptions{
		Name:              "gmessages-rag",
		Version:           "0.1.0",
		EnableSendMessage: cfg.EnableMCPSendMessage,
	})

	rootMux := BuildServeMux(database, gmClient, mcpServer, metrics)

	// 8. Start background batch ETL pipeline for any tracked contacts
	StartETLForTrackedContacts(ctx, logger, database, gmClient, cfg.TrackedContacts, cfg.MaxBackfillMessages)

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

	_ = watchdog
	logger.Info().Msg("Daemon stopped cleanly")
	return nil
}
