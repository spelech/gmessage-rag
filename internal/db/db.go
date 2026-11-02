package db

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	pgxvec "github.com/pgvector/pgvector-go/pgx"
	"github.com/rs/zerolog/log"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// DB wraps a pgxpool.Pool and exposes database operations.
type DB struct {
	Pool *pgxpool.Pool
}

// Connect establishes a connection pool to PostgreSQL using DATABASE_URL and registers pgvector types.
func Connect(ctx context.Context, databaseURL string) (*DB, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse database config: %w", err)
	}

	// Register pgvector types on each newly established connection
	config.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		return pgxvec.RegisterTypes(ctx, conn)
	}

	config.MaxConns = 25
	config.MinConns = 2
	config.MaxConnLifetime = 1 * time.Hour
	config.MaxConnIdleTime = 30 * time.Minute
	config.HealthCheckPeriod = 1 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("failed to create connection pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	log.Info().Msg("Connected to PostgreSQL database with pgvector registered")
	return &DB{Pool: pool}, nil
}

// New instantiates a DB connection pool (alias for Connect).
func New(ctx context.Context, databaseURL string) (*DB, error) {
	return Connect(ctx, databaseURL)
}

// Close closes the underlying connection pool.
func (db *DB) Close() {
	if db.Pool != nil {
		db.Pool.Close()
	}
}

// Ping verifies connectivity to the database.
func (db *DB) Ping(ctx context.Context) error {
	if db == nil || db.Pool == nil {
		return fmt.Errorf("database pool is not initialized")
	}
	return db.Pool.Ping(ctx)
}

// Migrate executes all embedded SQL migrations in lexical order.
func (db *DB) Migrate(ctx context.Context) error {
	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		return fmt.Errorf("failed to read embedded migrations directory: %w", err)
	}

	// Sort migration files lexically
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}

		filePath := "migrations/" + entry.Name()
		content, err := migrationFiles.ReadFile(filePath)
		if err != nil {
			return fmt.Errorf("failed to read migration file %s: %w", filePath, err)
		}

		log.Info().Str("migration", entry.Name()).Msg("Applying database migration")
		if _, err := db.Pool.Exec(ctx, string(content)); err != nil {
			return fmt.Errorf("failed to apply migration %s: %w", entry.Name(), err)
		}
	}

	log.Info().Msg("All database migrations applied successfully")
	return nil
}

// GetMigrationScripts returns the names and contents of all embedded migration files (useful for tests).
func GetMigrationScripts() (map[string]string, error) {
	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		return nil, err
	}

	scripts := make(map[string]string)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		filePath := "migrations/" + entry.Name()
		content, err := migrationFiles.ReadFile(filePath)
		if err != nil {
			return nil, err
		}
		scripts[entry.Name()] = string(content)
	}
	return scripts, nil
}
