package obs_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/spelech/gmessage-rag/internal/db"
	"github.com/spelech/gmessage-rag/internal/obs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockPinger implements obs.Pinger and obs.ActiveConnChecker for testing.
//
// Architectural Justification (Five-Question Decision Tree - Question 3 / Question 5):
// Deterministic timeout injection (simulating a slow/hanging ping that exceeds a 20ms context deadline)
// cannot be deterministically triggered against a local or remote PostgreSQL instance without causing flaky tests.
// A synthetic delay injection harness is retained exclusively for TestHealthHandler_Timeout to ensure reliable,
// non-flaky verification of context deadline cancellation. Real PostgreSQL connectivity is verified in
// TestHealthHandler_RealDB_Healthy and TestHealthHandler_RealDB_ClosedPool.
type mockPinger struct {
	pingErr     error
	activeConns int
	delay       time.Duration
}

func (m *mockPinger) Ping(ctx context.Context) error {
	if m.delay > 0 {
		select {
		case <-time.After(m.delay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return m.pingErr
}

func (m *mockPinger) ActiveConns() int {
	return m.activeConns
}

func TestHealthHandler_Healthy(t *testing.T) {
	pinger := &mockPinger{
		pingErr:     nil,
		activeConns: 5,
	}

	handler := obs.HealthHandler(pinger)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	resp := rec.Result()
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))

	var body obs.HealthResponse
	err := json.NewDecoder(resp.Body).Decode(&body)
	require.NoError(t, err)

	assert.Equal(t, "ok", body.Status)
	assert.Equal(t, "healthy", body.DB)
	require.NotNil(t, body.ActiveConns)
	assert.Equal(t, 5, *body.ActiveConns)
	assert.Empty(t, body.Error)
}

func TestHealthHandler_UnreachableDB(t *testing.T) {
	pinger := &mockPinger{
		pingErr: errors.New("connection refused"),
	}

	handler := obs.HealthHandler(pinger)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	resp := rec.Result()
	defer resp.Body.Close()

	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))

	var body obs.HealthResponse
	err := json.NewDecoder(resp.Body).Decode(&body)
	require.NoError(t, err)

	assert.Equal(t, "error", body.Status)
	assert.Equal(t, "unreachable", body.DB)
	assert.Contains(t, body.Error, "connection refused")
}

func TestHealthHandler_NilDB(t *testing.T) {
	handler := obs.HealthHandler(nil)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	resp := rec.Result()
	defer resp.Body.Close()

	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))

	var body obs.HealthResponse
	err := json.NewDecoder(resp.Body).Decode(&body)
	require.NoError(t, err)

	assert.Equal(t, "error", body.Status)
	assert.Equal(t, "unreachable", body.DB)
	assert.Contains(t, body.Error, "database handle is nil")
}

func TestHealthHandler_Timeout(t *testing.T) {
	pinger := &mockPinger{
		delay: 100 * time.Millisecond,
	}

	handler := obs.HealthHandler(pinger, obs.WithHealthTimeout(20*time.Millisecond))

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	resp := rec.Result()
	defer resp.Body.Close()

	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)

	var body obs.HealthResponse
	err := json.NewDecoder(resp.Body).Decode(&body)
	require.NoError(t, err)

	assert.Equal(t, "error", body.Status)
	assert.Equal(t, "unreachable", body.DB)
	assert.Contains(t, body.Error, context.DeadlineExceeded.Error())
}

// pingerWithoutActiveConns tests fallback when ActiveConnChecker is not implemented.
type pingerWithoutActiveConns struct{}

func (p *pingerWithoutActiveConns) Ping(ctx context.Context) error {
	return nil
}

func TestHealthHandler_WithoutActiveConns(t *testing.T) {
	handler := obs.HealthHandler(&pingerWithoutActiveConns{})

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	resp := rec.Result()
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var body obs.HealthResponse
	err := json.NewDecoder(resp.Body).Decode(&body)
	require.NoError(t, err)

	assert.Equal(t, "ok", body.Status)
	assert.Equal(t, "healthy", body.DB)
	assert.Nil(t, body.ActiveConns)
}

// Real-DB tests verifying obs.HealthHandler against a real PostgreSQL connection.

func openHealthTestDB(t *testing.T) *db.DB {
	t.Helper()
	connStr := os.Getenv("DATABASE_URL")
	if connStr == "" {
		connStr = "postgres://jules:postgres@localhost:5432/gmessages?sslmode=disable"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	database, err := db.New(ctx, connStr)
	if err != nil {
		t.Skipf("skipping real-DB health test: database connection failed: %v", err)
	}
	return database
}

func TestHealthHandler_RealDB_Healthy(t *testing.T) {
	database := openHealthTestDB(t)
	defer database.Close()

	handler := obs.HealthHandler(database)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	resp := rec.Result()
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))

	var body obs.HealthResponse
	err := json.NewDecoder(resp.Body).Decode(&body)
	require.NoError(t, err)

	assert.Equal(t, "ok", body.Status)
	assert.Equal(t, "healthy", body.DB)
	assert.Empty(t, body.Error)
}

func TestHealthHandler_RealDB_ClosedPool(t *testing.T) {
	database := openHealthTestDB(t)
	database.Close() // Close underlying pool to simulate unreachable database

	handler := obs.HealthHandler(database)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	resp := rec.Result()
	defer resp.Body.Close()

	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))

	var body obs.HealthResponse
	err := json.NewDecoder(resp.Body).Decode(&body)
	require.NoError(t, err)

	assert.Equal(t, "error", body.Status)
	assert.Equal(t, "unreachable", body.DB)
	assert.NotEmpty(t, body.Error)
}
