package db

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	pgxvec "github.com/pgvector/pgvector-go/pgx"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Config holds PostgreSQL connection pool and healthcheck settings.
type Config struct {
	DatabaseURL        string
	MaxConns           int32
	MinConns           int32
	MaxConnLifetime    time.Duration
	MaxConnIdleTime    time.Duration
	HealthCheckTimeout time.Duration
}

// DefaultConfig returns default connection pool settings.
func DefaultConfig(databaseURL string) Config {
	return Config{
		DatabaseURL:        databaseURL,
		MaxConns:           10,
		MinConns:           2,
		MaxConnLifetime:    time.Hour,
		MaxConnIdleTime:    30 * time.Minute,
		HealthCheckTimeout: 5 * time.Second,
	}
}

// DB wraps the pgxpool.Pool and exposes database operations.
type DB struct {
	pool *pgxpool.Pool
}

// Connect establishes a pgx connection pool, registers pgvector types,
// verifies database connectivity, and executes embedded migrations.
func Connect(ctx context.Context, cfg Config) (*DB, error) {
	if cfg.DatabaseURL == "" {
		return nil, fmt.Errorf("database URL is required")
	}

	// 1. Connect first with a single connection to bootstrap migrations and extensions
	bootstrapCfg, err := pgx.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse database configuration: %w", err)
	}

	initCtx := ctx
	if cfg.HealthCheckTimeout > 0 {
		var cancel context.CancelFunc
		initCtx, cancel = context.WithTimeout(ctx, cfg.HealthCheckTimeout)
		defer cancel()
	}

	initConn, err := pgx.ConnectConfig(initCtx, bootstrapCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to establish initial connection: %w", err)
	}

	// Ping database
	if err := initConn.Ping(initCtx); err != nil {
		_ = initConn.Close(ctx)
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	// Run embedded migrations using the bootstrap connection
	if err := runMigrationsOnConn(initCtx, initConn, migrationFS, "migrations"); err != nil {
		_ = initConn.Close(ctx)
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}
	_ = initConn.Close(ctx)

	// 2. Initialize the connection pool with pgvector registered AfterConnect
	poolCfg, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse pool configuration: %w", err)
	}

	if cfg.MaxConns > 0 {
		poolCfg.MaxConns = cfg.MaxConns
	}
	if cfg.MinConns > 0 {
		poolCfg.MinConns = cfg.MinConns
	}
	if cfg.MaxConnLifetime > 0 {
		poolCfg.MaxConnLifetime = cfg.MaxConnLifetime
	}
	if cfg.MaxConnIdleTime > 0 {
		poolCfg.MaxConnIdleTime = cfg.MaxConnIdleTime
	}

	// Register pgvector types on each newly established connection in the pool
	poolCfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		return pgxvec.RegisterTypes(ctx, conn)
	}

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create connection pool: %w", err)
	}

	database := &DB{pool: pool}

	// Verify pool connectivity with registered pgvector types
	pingCtx := ctx
	if cfg.HealthCheckTimeout > 0 {
		var cancel context.CancelFunc
		pingCtx, cancel = context.WithTimeout(ctx, cfg.HealthCheckTimeout)
		defer cancel()
	}
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("failed to ping pool after initialization: %w", err)
	}

	return database, nil
}

// Pool returns the underlying pgx connection pool.
func (d *DB) Pool() *pgxpool.Pool {
	return d.pool
}

// Ping checks pool responsiveness within the provided context.
func (d *DB) Ping(ctx context.Context) error {
	return d.pool.Ping(ctx)
}

// Close gracefully shuts down and drains the connection pool.
func (d *DB) Close() {
	if d.pool != nil {
		d.pool.Close()
	}
}

// Migrate applies pending SQL migrations from the embedded filesystem.
func (d *DB) Migrate(ctx context.Context) error {
	return RunMigrations(ctx, d.pool, migrationFS, "migrations")
}

// RunMigrations executes SQL files located in subDir of fsys against a pool.
// It ensures idempotency by tracking applied migrations in schema_migrations.
func RunMigrations(ctx context.Context, pool *pgxpool.Pool, fsys fs.FS, subDir string) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("failed to acquire connection for migrations: %w", err)
	}
	defer conn.Release()

	return runMigrationsOnConn(ctx, conn.Conn(), fsys, subDir)
}

func runMigrationsOnConn(ctx context.Context, conn *pgx.Conn, fsys fs.FS, subDir string) error {
	entries, err := fs.ReadDir(fsys, subDir)
	if err != nil {
		return fmt.Errorf("failed to read migrations directory: %w", err)
	}

	var files []string
	for _, entry := range entries {
		if !entry.IsDir() && len(entry.Name()) > 4 && entry.Name()[len(entry.Name())-4:] == ".sql" {
			files = append(files, entry.Name())
		}
	}
	sort.Strings(files)

	// Ensure tracking table exists
	createTableQuery := `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version TEXT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);
	`
	if _, err := conn.Exec(ctx, createTableQuery); err != nil {
		return fmt.Errorf("failed to create schema_migrations table: %w", err)
	}

	for _, filename := range files {
		var applied bool
		err := conn.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version = $1)", filename).Scan(&applied)
		if err != nil {
			return fmt.Errorf("failed to check migration status for %s: %w", filename, err)
		}
		if applied {
			continue
		}

		filePath := filename
		if subDir != "" && subDir != "." {
			filePath = subDir + "/" + filename
		}

		content, err := fs.ReadFile(fsys, filePath)
		if err != nil {
			return fmt.Errorf("failed to read migration file %s: %w", filename, err)
		}

		tx, err := conn.Begin(ctx)
		if err != nil {
			return fmt.Errorf("failed to start migration transaction for %s: %w", filename, err)
		}

		if _, err := tx.Exec(ctx, string(content)); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("failed to apply migration %s: %w", filename, err)
		}

		if _, err := tx.Exec(ctx, "INSERT INTO schema_migrations (version) VALUES ($1)", filename); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("failed to record migration %s: %w", filename, err)
		}

		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("failed to commit migration transaction for %s: %w", filename, err)
		}
	}

	return nil
}
