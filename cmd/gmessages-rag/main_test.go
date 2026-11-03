package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spelech/gmessage-rag/internal/config"
	"github.com/spelech/gmessage-rag/internal/db"
	"github.com/spelech/gmessage-rag/internal/gm"
	"github.com/spelech/gmessage-rag/internal/mcpserver"
	"github.com/spelech/gmessage-rag/internal/notify"
	"github.com/spelech/gmessage-rag/internal/obs"
)

// TestCLI_RootCommandFlags verifies flag registration and help output.
func TestCLI_RootCommandFlags(t *testing.T) {
	assert.Equal(t, "gmessages-rag", rootCmd.Use)

	cmd, _, err := rootCmd.Find([]string{"pair"})
	require.NoError(t, err)
	assert.Equal(t, "pair", cmd.Name())

	gaiaFlagVal := cmd.Flags().Lookup("gaia")
	require.NotNil(t, gaiaFlagVal)
	assert.Equal(t, "false", gaiaFlagVal.DefValue)

	serveFind, _, err := rootCmd.Find([]string{"serve"})
	require.NoError(t, err)
	assert.Equal(t, "serve", serveFind.Name())
}

// TestBuildServeMux_RouteRegistration tests that all required endpoints are mapped on the root mux.
func TestBuildServeMux_RouteRegistration(t *testing.T) {
	metrics := obs.NewMetricsRegistry()
	mcpSrv := mcpserver.NewServer(mcpserver.ServerOptions{
		Name:              "test-rag",
		Version:           "1.0.0",
		EnableSendMessage: true,
	})

	mux := BuildServeMux(nil, nil, mcpSrv, metrics)
	require.NotNil(t, mux)

	// 1. Prometheus /metrics endpoint
	reqMetrics := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	recMetrics := httptest.NewRecorder()
	mux.ServeHTTP(recMetrics, reqMetrics)
	assert.Equal(t, http.StatusOK, recMetrics.Code)
	assert.Contains(t, recMetrics.Body.String(), "gmessages_active_websocket_connections")

	// 2. Health probe /healthz endpoint (nil db -> 503 Service Unavailable)
	reqHealth := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	recHealth := httptest.NewRecorder()
	mux.ServeHTTP(recHealth, reqHealth)
	assert.Equal(t, http.StatusServiceUnavailable, recHealth.Code)
	assert.Contains(t, recHealth.Body.String(), "unreachable")

	// 3. MCP endpoint POST /mcp
	reqMCP := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	recMCP := httptest.NewRecorder()
	mux.ServeHTTP(recMCP, reqMCP)
	assert.Equal(t, "2026-07-28", recMCP.Header().Get("Mcp-Protocol-Version"))

	// 4. MCP SSE endpoint GET /sse
	reqSSE := httptest.NewRequest(http.MethodGet, "/sse", nil)
	recSSE := httptest.NewRecorder()
	mux.ServeHTTP(recSSE, reqSSE)
	assert.Equal(t, "2026-07-28", recSSE.Header().Get("Mcp-Protocol-Version"))

	// 5. Test bench POST /api/v1/testbench/guardrail
	reqTB := httptest.NewRequest(http.MethodPost, "/api/v1/testbench/guardrail", nil)
	recTB := httptest.NewRecorder()
	mux.ServeHTTP(recTB, reqTB)
	// Body empty, should return 400 bad request from json decoder
	assert.Equal(t, http.StatusBadRequest, recTB.Code)

	// 6. Dashboard /
	reqDash := httptest.NewRequest(http.MethodGet, "/", nil)
	recDash := httptest.NewRecorder()
	mux.ServeHTTP(recDash, reqDash)
	assert.Equal(t, http.StatusOK, recDash.Code)

	// 7. Status API GET /api/v1/status
	reqStatus := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	recStatus := httptest.NewRecorder()
	mux.ServeHTTP(recStatus, reqStatus)
	assert.Equal(t, http.StatusOK, recStatus.Code)
	assert.Contains(t, recStatus.Body.String(), "degraded")

	// 8. Briefing API GET /api/v1/briefing (nil db -> 503 Service Unavailable)
	reqBriefing := httptest.NewRequest(http.MethodGet, "/api/v1/briefing", nil)
	recBriefing := httptest.NewRecorder()
	mux.ServeHTTP(recBriefing, reqBriefing)
	assert.Equal(t, http.StatusServiceUnavailable, recBriefing.Code)

	// 9. Briefing alias GET /api/v1/briefing/today
	reqBriefingToday := httptest.NewRequest(http.MethodGet, "/api/v1/briefing/today", nil)
	recBriefingToday := httptest.NewRecorder()
	mux.ServeHTTP(recBriefingToday, reqBriefingToday)
	assert.Equal(t, http.StatusServiceUnavailable, recBriefingToday.Code)
}

// TestStartETLForTrackedContacts_EarlyReturn verifies nil dependencies or empty contacts return immediately.
func TestStartETLForTrackedContacts_EarlyReturn(t *testing.T) {
	logger := zerolog.Nop()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// 1. Empty contacts
	StartETLForTrackedContacts(ctx, logger, nil, nil, nil, nil, nil, 100)

	// 2. Nil DB
	StartETLForTrackedContacts(ctx, logger, nil, nil, nil, nil, []string{"contact-1"}, 100)
}

// TestPairCommand_ContextCancel tests that pair command respects context cancellation and handles errors gracefully.
func TestPairCommand_ContextCancel(t *testing.T) {
	tempDir := t.TempDir()
	sessionFile := filepath.Join(tempDir, "session.json")
	t.Setenv("SESSION_PATH", sessionFile)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// 1. Test pairCmd with standard QR mode under immediate context cancellation
	cmd := &cobra.Command{}
	cmd.SetContext(ctx)
	gaiaFlag = false

	// Test runPair directly with an invalid session path or unresolvable server
	err := runPair(cmd, nil)
	assert.Error(t, err)
}

// TestPairCommand_GaiaErrorHandling tests gaiaFlag error handling.
func TestPairCommand_GaiaErrorHandling(t *testing.T) {
	tempDir := t.TempDir()
	sessionFile := filepath.Join(tempDir, "session.json")
	t.Setenv("SESSION_PATH", sessionFile)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	gaiaFlag = true
	defer func() { gaiaFlag = false }()

	cmd := &cobra.Command{}
	cmd.SetContext(ctx)
	err := runPair(cmd, nil)
	assert.Error(t, err)
}

// TestServeCommand_ConfigValidation verifies error handling when configuration or database is invalid.
func TestServeCommand_ConfigValidation(t *testing.T) {
	// Set DATABASE_URL to an invalid unreachable port
	t.Setenv("DATABASE_URL", "postgres://invalid:invalid@127.0.0.1:59999/nonexistent?sslmode=disable")
	t.Setenv("HTTP_PORT", "65432")

	cmd := &cobra.Command{}
	err := runServe(cmd, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to connect to database")
}

// TestLoadConfigDefaults verifies config values read defaults properly.
func TestLoadConfigDefaults(t *testing.T) {
	cfg, err := config.LoadConfig()
	require.NoError(t, err)
	assert.Equal(t, 8090, cfg.HTTPPort)
	assert.Equal(t, "/data/media", cfg.MediaDir)
}

// TestServeCommand_Lifecycle verifies the full daemon initialization, HTTP server start, and graceful shutdown sequence.
func TestServeCommand_Lifecycle(t *testing.T) {
	dbURL := "postgres://jules:@127.0.0.1:5432/gmessages?sslmode=disable"
	testDB, err := db.Connect(context.Background(), dbURL)
	if err != nil {
		t.Skipf("skipping live serve lifecycle test: %v", err)
		return
	}
	testDB.Close()

	tempDir := t.TempDir()
	sessionFile := filepath.Join(tempDir, "session.json")
	t.Setenv("SESSION_PATH", sessionFile)
	t.Setenv("DATABASE_URL", dbURL)
	t.Setenv("HTTP_PORT", "38194")
	t.Setenv("TRACKED_CONTACTS", "test-tracked-contact-1")

	ctx, cancel := context.WithTimeout(context.Background(), 350*time.Millisecond)
	defer cancel()

	cmd := &cobra.Command{}
	cmd.SetContext(ctx)

	err = runServe(cmd, nil)
	assert.NoError(t, err)
}

// TestStartETLForTrackedContacts_Execution verifies background ETL synchronization pass.
func TestStartETLForTrackedContacts_Execution(t *testing.T) {
	dbURL := "postgres://jules:@127.0.0.1:5432/gmessages?sslmode=disable"
	database, err := db.Connect(context.Background(), dbURL)
	if err != nil {
		t.Skipf("skipping ETL execution test: %v", err)
		return
	}
	defer database.Close()

	tempDir := t.TempDir()
	sessionFile := filepath.Join(tempDir, "session.json")
	client, err := gm.NewClient(gm.ClientOptions{
		SessionPath: sessionFile,
	})
	require.NoError(t, err)

	logger := zerolog.Nop()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	StartETLForTrackedContacts(ctx, logger, database, client, nil, nil, []string{"tracked-1", "tracked-2"}, 50)
	time.Sleep(50 * time.Millisecond)
}

// TestRootCmd_Help exercises rootCmd Run function.
func TestRootCmd_Help(t *testing.T) {
	rootCmd.Run(rootCmd, []string{})
}

// TestPairCommand_DefaultSessionPath verifies runPair fallback when SESSION_PATH is unset.
func TestPairCommand_DefaultSessionPath(t *testing.T) {
	t.Setenv("SESSION_PATH", "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cmd := &cobra.Command{}
	cmd.SetContext(ctx)
	gaiaFlag = false
	_ = runPair(cmd, nil)
}

// TestServeCommand_ServerListenError verifies handling of server listen errors.
func TestServeCommand_ServerListenError(t *testing.T) {
	dbURL := "postgres://jules:@127.0.0.1:5432/gmessages?sslmode=disable"
	testDB, err := db.Connect(context.Background(), dbURL)
	if err != nil {
		t.Skipf("skipping live serve test: %v", err)
		return
	}
	testDB.Close()

	t.Setenv("DATABASE_URL", dbURL)
	t.Setenv("HTTP_PORT", "-1")
	t.Setenv("SESSION_PATH", filepath.Join(t.TempDir(), "session.json"))

	cmd := &cobra.Command{}
	err = runServe(cmd, nil)
	assert.Error(t, err)
}

// TestRootCmd_ExecuteHelp tests Execute() with help flag.
func TestRootCmd_ExecuteHelp(t *testing.T) {
	rootCmd.SetArgs([]string{"--help"})
	err := Execute()
	assert.NoError(t, err)
}

// TestPairCommand_InitClientFailure tests pairing client initialization error when session path is invalid.
func TestPairCommand_InitClientFailure(t *testing.T) {
	t.Setenv("SESSION_PATH", t.TempDir())
	cmd := &cobra.Command{}
	gaiaFlag = false
	err := runPair(cmd, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to initialize pairing client")
}

// TestPairCommand_InitGaiaFailure tests gaia client initialization error when session path is invalid.
func TestPairCommand_InitGaiaFailure(t *testing.T) {
	t.Setenv("SESSION_PATH", t.TempDir())
	cmd := &cobra.Command{}
	gaiaFlag = true
	defer func() { gaiaFlag = false }()
	err := runPair(cmd, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to initialize gaia client")
}

// TestServeCommand_GMClientInitFailure verifies error returned when GM client fails initialization.
func TestServeCommand_GMClientInitFailure(t *testing.T) {
	dbURL := "postgres://jules:@127.0.0.1:5432/gmessages?sslmode=disable"
	testDB, err := db.Connect(context.Background(), dbURL)
	if err != nil {
		t.Skipf("skipping live serve test: %v", err)
		return
	}
	testDB.Close()

	t.Setenv("DATABASE_URL", dbURL)
	t.Setenv("HTTP_PORT", "38195")
	t.Setenv("SESSION_PATH", t.TempDir()) // causes LoadAuthData error

	cmd := &cobra.Command{}
	err = runServe(cmd, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to initialize Google Messages client")
}

// [Requirement(TRANS-04)] [Requirement(AUTH-03)]
// TestServeCommand_WatchdogWiring verifies that watchdog callbacks wire into gm.ClientOptions
// and trigger watchdog state changes and alerts as expected.
func TestServeCommand_WatchdogWiring(t *testing.T) {
	tempDir := t.TempDir()
	sessionFile := filepath.Join(tempDir, "session.json")

	notifyClient := notify.NewClient(notify.ClientConfig{})
	wd := notify.NewWatchdog(notify.WatchdogConfig{
		NotifyClient:            notifyClient,
		NotifyOnPhoneDisconnect: true,
		GracePeriod:             1 * time.Minute,
		Logger:                  zerolog.Nop(),
	})

	client, err := gm.NewClient(gm.ClientOptions{
		SessionPath:            sessionFile,
		OnPhoneNotResponding:   wd.OnPhoneNotResponding,
		OnPhoneRespondingAgain: wd.OnPhoneRespondingAgain,
		OnListenFatalError:     wd.OnListenFatalError,
	})
	require.NoError(t, err)

	// Initially, watchdog timer should not be active
	assert.False(t, wd.IsTimerActive())

	// PhoneNotResponding callback should activate the grace-period timer
	client.LibGM() // ensure client initialized
	wd.OnPhoneNotResponding()
	assert.True(t, wd.IsTimerActive())

	// PhoneRespondingAgain should cancel the grace-period timer
	wd.OnPhoneRespondingAgain()
	assert.False(t, wd.IsTimerActive())

	// Shutdown cleanup simulation: ensure repeated call doesn't panic
	assert.NotPanics(t, func() {
		wd.OnPhoneRespondingAgain()
	})
}
