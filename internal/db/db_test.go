package db_test

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/pgvector/pgvector-go"
	"github.com/spelech/gmessage-rag/internal/db"
)

func getTestDatabaseURL(t *testing.T) string {
	baseURL := os.Getenv("DATABASE_URL")
	if baseURL == "" {
		baseURL = "postgres://rcsrag:changeme@127.0.0.1:5432/gmessages?sslmode=disable"
	}
	return baseURL
}

// createEphemeralDB creates an isolated database for a test and returns its connection URL and cleanup function.
func createEphemeralDB(t *testing.T) (string, func()) {
	baseURL := getTestDatabaseURL(t)
	cfg, err := pgx.ParseConfig(baseURL)
	if err != nil {
		t.Fatalf("failed to parse base database URL: %v", err)
	}

	// Connect to default 'postgres' database to create an isolated test db
	adminCfg, _ := pgx.ParseConfig(baseURL)
	adminCfg.Database = "postgres"

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	adminConn, err := pgx.ConnectConfig(ctx, adminCfg)
	if err != nil {
		t.Fatalf("failed to connect to admin database: %v", err)
	}

	testDBName := fmt.Sprintf("test_db_%d_%d", time.Now().UnixNano(), rand.Intn(10000))
	_, err = adminConn.Exec(ctx, fmt.Sprintf("CREATE DATABASE %s OWNER %s;", testDBName, cfg.User))
	if err != nil {
		adminConn.Close(ctx)
		t.Fatalf("failed to create ephemeral database %s: %v", testDBName, err)
	}

	testURL := fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=disable",
		cfg.User, cfg.Password, cfg.Host, cfg.Port, testDBName)

	cleanup := func() {
		cleanCtx, cleanCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanCancel()

		// Disconnect all sessions to testDBName
		_, _ = adminConn.Exec(cleanCtx, fmt.Sprintf(
			"SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '%s';", testDBName))
		_, _ = adminConn.Exec(cleanCtx, fmt.Sprintf("DROP DATABASE IF EXISTS %s;", testDBName))
		_ = adminConn.Close(cleanCtx)
	}

	return testURL, cleanup
}

// [Requirement("DB-01")]
// TestDatabaseMigrations asserts that Connect automatically applies embedded migrations,
// creating all required extensions and tables.
func TestDatabaseMigrations(t *testing.T) {
	testURL, cleanup := createEphemeralDB(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	cfg := db.DefaultConfig(testURL)
	database, err := db.Connect(ctx, cfg)
	if err != nil {
		t.Fatalf("db.Connect failed: %v", err)
	}
	defer database.Close()

	// Verify required extensions
	var vectorExt, trgmExt bool
	err = database.Pool().QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_extension WHERE extname = 'vector')").Scan(&vectorExt)
	if err != nil || !vectorExt {
		t.Errorf("expected extension 'vector' to exist, got %v (err: %v)", vectorExt, err)
	}

	err = database.Pool().QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_extension WHERE extname = 'pg_trgm')").Scan(&trgmExt)
	if err != nil || !trgmExt {
		t.Errorf("expected extension 'pg_trgm' to exist, got %v (err: %v)", trgmExt, err)
	}

	// Verify required tables exist
	requiredTables := []string{
		"schema_migrations",
		"conversations",
		"conversation_sessions",
		"messages",
		"media_attachments",
		"contact_dossiers",
		"contact_dossier_history",
	}

	for _, table := range requiredTables {
		var exists bool
		err := database.Pool().QueryRow(ctx,
			"SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_schema = 'public' AND table_name = $1)",
			table).Scan(&exists)
		if err != nil {
			t.Fatalf("error checking existence of table %s: %v", table, err)
		}
		if !exists {
			t.Errorf("expected table %s to exist in public schema", table)
		}
	}

	// Verify idempotent re-run of Migrate
	if err := database.Migrate(ctx); err != nil {
		t.Fatalf("re-running Migrate() should be idempotent, got error: %v", err)
	}
}

// [Requirement("DB-01")]
// TestVectorSerialization tests that pgvector types (especially halfvec) serialize and deserialize cleanly.
func TestVectorSerialization(t *testing.T) {
	testURL, cleanup := createEphemeralDB(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	database, err := db.Connect(ctx, db.DefaultConfig(testURL))
	if err != nil {
		t.Fatalf("db.Connect failed: %v", err)
	}
	defer database.Close()

	// Seed conversation
	convID := "conv_vector_test"
	_, err = database.Pool().Exec(ctx, `
		INSERT INTO conversations (id, name, participants, is_tracked)
		VALUES ($1, 'Test Vector Contact', '[]'::jsonb, true)
	`, convID)
	if err != nil {
		t.Fatalf("failed to insert test conversation: %v", err)
	}

	// Insert session with halfvec(1536) embedding
	vecData := make([]float32, 1536)
	vecData[0] = 0.123
	vecData[1] = -0.456
	vecData[1535] = 0.789
	halfVec := pgvector.NewHalfVector(vecData)

	var sessionID int64
	err = database.Pool().QueryRow(ctx, `
		INSERT INTO conversation_sessions (conversation_id, started_at, ended_at, transcript, embedding)
		VALUES ($1, NOW(), NOW() + INTERVAL '1 hour', 'Test transcript', $2)
		RETURNING id
	`, convID, halfVec).Scan(&sessionID)
	if err != nil {
		t.Fatalf("failed to insert session with halfvec embedding: %v", err)
	}

	// Read back halfvec embedding
	var retrievedVec pgvector.HalfVector
	err = database.Pool().QueryRow(ctx, `
		SELECT embedding FROM conversation_sessions WHERE id = $1
	`, sessionID).Scan(&retrievedVec)
	if err != nil {
		t.Fatalf("failed to read back halfvec embedding: %v", err)
	}

	retrievedSlice := retrievedVec.Slice()
	if len(retrievedSlice) != 1536 {
		t.Fatalf("expected 1536 dimensions, got %d", len(retrievedSlice))
	}

	// Compare with tolerance for half-precision floating point
	const epsilon = 1e-3
	diff0 := retrievedSlice[0] - vecData[0]
	if diff0 < -epsilon || diff0 > epsilon {
		t.Errorf("dim 0 mismatch: expected %f, got %f", vecData[0], retrievedSlice[0])
	}
	diff1 := retrievedSlice[1] - vecData[1]
	if diff1 < -epsilon || diff1 > epsilon {
		t.Errorf("dim 1 mismatch: expected %f, got %f", vecData[1], retrievedSlice[1])
	}
	diffLast := retrievedSlice[1535] - vecData[1535]
	if diffLast < -epsilon || diffLast > epsilon {
		t.Errorf("dim 1535 mismatch: expected %f, got %f", vecData[1535], retrievedSlice[1535])
	}
}

// [Requirement("DB-01")]
// TestPoolLifecycleAndDrain tests pool configuration, connection drain, and Close.
func TestPoolLifecycleAndDrain(t *testing.T) {
	testURL, cleanup := createEphemeralDB(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cfg := db.Config{
		DatabaseURL:        testURL,
		MaxConns:           5,
		MinConns:           1,
		MaxConnLifetime:    5 * time.Minute,
		MaxConnIdleTime:    1 * time.Minute,
		HealthCheckTimeout: 2 * time.Second,
	}

	database, err := db.Connect(ctx, cfg)
	if err != nil {
		t.Fatalf("db.Connect failed: %v", err)
	}

	if err := database.Ping(ctx); err != nil {
		t.Fatalf("expected Ping to succeed: %v", err)
	}

	stat := database.Pool().Stat()
	if stat.MaxConns() != 5 {
		t.Errorf("expected MaxConns 5, got %d", stat.MaxConns())
	}

	// Graceful close
	database.Close()

	// After close, ping should fail
	closedCtx, closedCancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer closedCancel()
	if err := database.Ping(closedCtx); err == nil {
		t.Errorf("expected Ping after Close to fail, but it succeeded")
	}
}

// [Requirement("DB-01")]
// TestMigrationRunnerErrorHandling tests error paths in migration runner.
func TestMigrationRunnerErrorHandling(t *testing.T) {
	testURL, cleanup := createEphemeralDB(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Test 1: Connect with invalid URL
	_, err := db.Connect(ctx, db.Config{DatabaseURL: "postgres://invalid:invalid@127.0.0.1:9999/nonexistent?sslmode=disable"})
	if err == nil {
		t.Fatalf("expected connection error for invalid URL, got nil")
	}

	// Test 2: Connect with empty URL
	_, err = db.Connect(ctx, db.Config{DatabaseURL: ""})
	if err == nil {
		t.Fatalf("expected error for empty database URL, got nil")
	}

	// Connect to valid DB to test migration errors
	database, err := db.Connect(ctx, db.DefaultConfig(testURL))
	if err != nil {
		t.Fatalf("db.Connect failed: %v", err)
	}
	defer database.Close()

	// Test 3: RunMigrations with missing directory
	mockFS := fstest.MapFS{}
	err = db.RunMigrations(ctx, database.Pool(), mockFS, "nonexistent_dir")
	if err == nil {
		t.Errorf("expected error for nonexistent migration dir, got nil")
	}

	// Test 4: RunMigrations with invalid SQL syntax
	badSQLFS := fstest.MapFS{
		"migrations/002_broken.sql": &fstest.MapFile{
			Data: []byte("THIS IS INVALID SQL;"),
		},
	}
	err = db.RunMigrations(ctx, database.Pool(), badSQLFS, "migrations")
	if err == nil {
		t.Errorf("expected error executing broken SQL migration, got nil")
	}

	// Verify broken migration was not recorded in schema_migrations
	var count int
	_ = database.Pool().QueryRow(ctx, "SELECT count(*) FROM schema_migrations WHERE version = '002_broken.sql'").Scan(&count)
	if count != 0 {
		t.Errorf("broken migration should not have been recorded in schema_migrations")
	}
}
