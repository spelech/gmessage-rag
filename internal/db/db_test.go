package db_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/spelech/gmessage-rag/internal/db"
)

func TestGetMigrationScripts(t *testing.T) {
	scripts, err := db.GetMigrationScripts()
	if err != nil {
		t.Fatalf("failed to get migration scripts: %v", err)
	}

	initSQL, ok := scripts["001_init.sql"]
	if !ok {
		t.Fatalf("expected 001_init.sql to be embedded in migrations, found: %v", scripts)
	}

	expectedKeywords := []string{
		"CREATE EXTENSION IF NOT EXISTS vector",
		"CREATE EXTENSION IF NOT EXISTS pg_trgm",
		"CREATE TABLE IF NOT EXISTS conversations",
		"CREATE TABLE IF NOT EXISTS conversation_sessions",
		"CREATE TABLE IF NOT EXISTS messages",
		"CREATE TABLE IF NOT EXISTS media_attachments",
		"CREATE TABLE IF NOT EXISTS contact_dossiers",
		"CREATE TABLE IF NOT EXISTS contact_dossier_history",
		"halfvec(1536)",
		"hnsw (embedding halfvec_cosine_ops)",
	}

	for _, kw := range expectedKeywords {
		if !strings.Contains(initSQL, kw) {
			t.Errorf("migration script missing expected keyword: %q", kw)
		}
	}
}

func TestDB_PingUninitialized(t *testing.T) {
	database := &db.DB{Pool: nil}
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	if err := database.Ping(ctx); err == nil {
		t.Errorf("expected error pinging nil pool, got nil")
	}
}

func TestConnect_InvalidURLFailsClosed(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := db.Connect(ctx, "invalid://url:not_a_port")
	if err == nil {
		t.Errorf("expected error connecting with invalid URL, got nil")
	}
}
