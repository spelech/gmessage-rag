package obs

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// Pinger defines the interface for database connectivity checking.
type Pinger interface {
	Ping(ctx context.Context) error
}

// ActiveConnChecker defines an optional interface for reporting active database connection status.
type ActiveConnChecker interface {
	ActiveConns() int
}

// HealthResponse represents the standardized JSON response body for the health probe.
type HealthResponse struct {
	Status      string `json:"status"`
	DB          string `json:"db"`
	ActiveConns *int   `json:"active_connections,omitempty"`
	Error       string `json:"error,omitempty"`
}

// HealthHandlerConfig configures the health probe handler.
type HealthHandlerConfig struct {
	Timeout time.Duration
}

// HealthHandlerOption specifies functional options for HealthHandler.
type HealthHandlerOption func(*HealthHandlerConfig)

// WithHealthTimeout sets a custom context timeout for the database ping.
func WithHealthTimeout(timeout time.Duration) HealthHandlerOption {
	return func(cfg *HealthHandlerConfig) {
		if timeout > 0 {
			cfg.Timeout = timeout
		}
	}
}

// NewHealthHandler constructs an http.Handler checking DB and optionally client status.
func NewHealthHandler(db Pinger, opts ...HealthHandlerOption) http.Handler {
	return HealthHandler(db, opts...)
}

// HealthHandler returns an http.HandlerFunc that verifies database pool reachability (db.Ping(ctx))
// and reports active connection status according to OBS-01.
// Returns HTTP 200 {"status": "ok", "db": "healthy"} or HTTP 503 if unreachable.
func HealthHandler(db Pinger, opts ...HealthHandlerOption) http.HandlerFunc {
	cfg := HealthHandlerConfig{
		Timeout: 3 * time.Second,
	}
	for _, opt := range opts {
		opt(&cfg)
	}

	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if db == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(HealthResponse{
				Status: "error",
				DB:     "unreachable",
				Error:  "database handle is nil",
			})
			return
		}

		ctx := r.Context()
		if cfg.Timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, cfg.Timeout)
			defer cancel()
		}

		if err := db.Ping(ctx); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(HealthResponse{
				Status: "error",
				DB:     "unreachable",
				Error:  err.Error(),
			})
			return
		}

		resp := HealthResponse{
			Status: "ok",
			DB:     "healthy",
		}

		if checker, ok := db.(ActiveConnChecker); ok {
			conns := checker.ActiveConns()
			resp.ActiveConns = &conns
		}

		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(resp)
	}
}
