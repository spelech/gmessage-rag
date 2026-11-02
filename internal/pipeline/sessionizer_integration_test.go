package pipeline_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"

	"github.com/spelech/gmessage-rag/internal/db"
	"github.com/spelech/gmessage-rag/internal/pipeline"
)

// TestSessionizer_PostgresIntegration exercises the sessionizer end-to-end against a real
// Postgres+pgvector database. The only stubbed boundary is the external OpenAI-compatible HTTP
// endpoint (a contract stub). This deliberately avoids mocking the internal DB dependency.
//
// [Requirement(PIPE-06)] [Requirement(DB-03)]
func TestSessionizer_PostgresIntegration(t *testing.T) {
	connStr := os.Getenv("DATABASE_URL")
	if connStr == "" {
		connStr = "postgres://jules:postgres@localhost:5432/gmessages?sslmode=disable"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	database, err := db.New(ctx, connStr)
	if err != nil {
		t.Skipf("skipping test: database connection failed: %v", err)
	}
	defer database.Close()

	if err := database.Migrate(ctx); err != nil {
		t.Fatalf("database migration failed: %v", err)
	}

	// Contract stub for the external LLM HTTP boundary (chat completions + embeddings).
	var chatCalls, embCalls int32
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/chat/completions":
			atomic.AddInt32(&chatCalls, 1)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{
					{
						"index": 0,
						"message": map[string]string{
							"role":    "assistant",
							"content": `{"title": "Integration Session", "summary": "Two people coordinate plans."}`,
						},
					},
				},
			})
		case "/embeddings":
			atomic.AddInt32(&embCalls, 1)
			vec := make([]float32, 1536)
			vec[0] = 0.25
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"index": 0, "embedding": vec}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer stub.Close()

	llm := pipeline.NewOpenAIClient(pipeline.LLMClientConfig{
		LLMBaseURL:          stub.URL,
		LLMAPIKey:           "test-key",
		SummaryModel:        "test-summary",
		EmbeddingBaseURL:    stub.URL,
		EmbeddingAPIKey:     "test-key",
		EmbeddingModel:      "test-embedding",
		EmbeddingDimensions: 1536,
	})

	sessionizer := pipeline.NewSessionizer(pipeline.SessionizerConfig{
		Store:           database,
		LLMClient:       llm,
		SessionGapHours: 2,
		Location:        time.UTC,
		Logger:          zerolog.Nop(),
	})

	// Seed a conversation with four messages that must split into two sessions (>2h gap).
	convID := fmt.Sprintf("conv-int-%d", time.Now().UnixNano())
	_, err = database.Pool.Exec(ctx, `
		INSERT INTO conversations (id, name, participants, is_group, is_tracked)
		VALUES ($1, $2, '["+15550001111"]'::jsonb, false, true)
	`, convID, "Integration Contact")
	require.NoError(t, err)

	base := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	sentAt := []time.Time{
		base,
		base.Add(10 * time.Minute),
		base.Add(5 * time.Hour),
		base.Add(5*time.Hour + 15*time.Minute),
	}
	for i, ts := range sentAt {
		id := fmt.Sprintf("%s-m%d", convID, i)
		_, err := database.Pool.Exec(ctx, `
			INSERT INTO messages (id, conversation_id, sender_id, sender_name, is_from_me, sent_at, body)
			VALUES ($1, $2, 'peer', 'Peer', false, $3, $4)
		`, id, convID, ts, fmt.Sprintf("Message body %d", i))
		require.NoError(t, err)
	}

	sessions, err := sessionizer.ProcessUnassignedMessages(ctx, convID)
	require.NoError(t, err)
	require.Len(t, sessions, 2, "expected the >2h gap to split the thread into two sessions")

	// Observable DB state: sessions persisted.
	var rowCount int
	require.NoError(t, database.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM conversation_sessions WHERE conversation_id = $1`, convID).Scan(&rowCount))
	require.Equal(t, 2, rowCount)

	// First session must carry a summary, an embedding, and both its messages.
	var (
		msgCount  int
		summary   *string
		embedding *string
	)
	require.NoError(t, database.Pool.QueryRow(ctx, `
		SELECT message_count, summary, embedding::text
		FROM conversation_sessions
		WHERE conversation_id = $1
		ORDER BY started_at ASC
		LIMIT 1
	`, convID).Scan(&msgCount, &summary, &embedding))
	require.Equal(t, 2, msgCount)
	require.NotNil(t, summary)
	require.NotNil(t, embedding)

	// No message may remain unassigned to a session.
	var unassigned int
	require.NoError(t, database.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM messages WHERE conversation_id = $1 AND session_id IS NULL`, convID).Scan(&unassigned))
	require.Equal(t, 0, unassigned)

	// The external LLM boundary must have been exercised exactly once per session.
	require.Equal(t, int32(2), atomic.LoadInt32(&chatCalls))
	require.Equal(t, int32(2), atomic.LoadInt32(&embCalls))
}
