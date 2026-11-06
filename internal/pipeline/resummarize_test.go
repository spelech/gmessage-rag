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

func openResummarizeTestDB(t *testing.T) *db.DB {
	t.Helper()
	connStr := os.Getenv("DATABASE_URL")
	if connStr == "" {
		connStr = "postgres://jules:postgres@localhost:5432/gmessages?sslmode=disable"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	database, err := db.New(ctx, connStr)
	if err != nil {
		t.Skipf("skipping test: database connection failed: %v", err)
	}
	if err := database.Migrate(ctx); err != nil {
		database.Close()
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(database.Close)
	return database
}

type resummarizeStub struct {
	server    *httptest.Server
	chatCalls int32
	embCalls  int32
}

func newResummarizeStub(t *testing.T) *resummarizeStub {
	t.Helper()
	s := &resummarizeStub{}
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/chat/completions":
			atomic.AddInt32(&s.chatCalls, 1)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{{
					"index": 0,
					"message": map[string]string{
						"role":    "assistant",
						"content": `{"title":"Resummarized","summary":"Regenerated summary."}`,
					},
				}},
			})
		case "/embeddings":
			atomic.AddInt32(&s.embCalls, 1)
			vec := make([]float32, 1536)
			vec[0] = 0.5
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"index": 0, "embedding": vec}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.server.Close)
	return s
}

func (s *resummarizeStub) client() pipeline.LLMClient {
	return pipeline.NewOpenAIClient(pipeline.LLMClientConfig{
		LLMBaseURL:          s.server.URL,
		LLMAPIKey:           "test-key",
		SummaryModel:        "test-summary",
		EmbeddingBaseURL:    s.server.URL,
		EmbeddingAPIKey:     "test-key",
		EmbeddingModel:      "test-embedding",
		EmbeddingDimensions: 1536,
	})
}

// TestSessionizer_ResummarizeMissing exercises re-summarization against a real
// Postgres+pgvector database. The only stub is the external LLM HTTP boundary.
//
// [Requirement(PIPE-07)]
func TestSessionizer_ResummarizeMissing(t *testing.T) {
	database := openResummarizeTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	stub := newResummarizeStub(t)
	sessionizer := pipeline.NewSessionizer(pipeline.SessionizerConfig{
		Store:           database,
		LLMClient:       stub.client(),
		SessionGapHours: 2,
		Location:        time.UTC,
		Logger:          zerolog.Nop(),
	})

	convID := fmt.Sprintf("conv-resum-%d", time.Now().UnixNano())
	_, err := database.Pool.Exec(ctx,
		`INSERT INTO conversations (id, name, participants, is_group, is_tracked) VALUES ($1, $2, '[]'::jsonb, false, true)`,
		convID, "Resummarize Contact")
	require.NoError(t, err)

	base := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)

	// Session A: missing summary and embedding, with two messages.
	var sessA int64
	require.NoError(t, database.Pool.QueryRow(ctx, `
		INSERT INTO conversation_sessions (conversation_id, started_at, ended_at, message_count, transcript, is_processed)
		VALUES ($1, $2, $3, 2, 'stub transcript', false) RETURNING id`,
		convID, base, base.Add(10*time.Minute)).Scan(&sessA))
	for i := 0; i < 2; i++ {
		_, err := database.Pool.Exec(ctx, `
			INSERT INTO messages (id, conversation_id, session_id, sender_id, sender_name, is_from_me, sent_at, body)
			VALUES ($1, $2, $3, 'peer', 'Peer', false, $4, $5)`,
			fmt.Sprintf("%s-a%d", convID, i), convID, sessA, base.Add(time.Duration(i)*time.Minute), fmt.Sprintf("hello %d", i))
		require.NoError(t, err)
	}

	// Session B: missing analysis with a counted message but no linked messages.
	var sessB int64
	require.NoError(t, database.Pool.QueryRow(ctx, `
		INSERT INTO conversation_sessions (conversation_id, started_at, ended_at, message_count, transcript, is_processed)
		VALUES ($1, $2, $3, 1, 'stub transcript', false) RETURNING id`,
		convID, base.Add(3*time.Hour), base.Add(3*time.Hour)).Scan(&sessB))

	// Session D: already fully analyzed -> must be excluded from the pass.
	var sessD int64
	existing := "Existing: already summarized."
	require.NoError(t, database.Pool.QueryRow(ctx, `
		INSERT INTO conversation_sessions (conversation_id, started_at, ended_at, message_count, summary, embedding, transcript, is_processed)
		VALUES ($1, $2, $3, 1, $4, array_fill(0::real, ARRAY[1536])::halfvec, 'stub transcript', true) RETURNING id`,
		convID, base.Add(5*time.Hour), base.Add(5*time.Hour), existing).Scan(&sessD))

	report, err := sessionizer.ResummarizeMissing(ctx, convID, 100)
	require.NoError(t, err)
	require.Equal(t, 2, report.Requested, "A and B need analysis; D is already analyzed")
	require.Equal(t, 1, report.Updated)
	require.Equal(t, 1, report.Skipped, "orphaned session B has no messages")
	require.Equal(t, 0, report.Failed)
	require.Equal(t, int32(1), atomic.LoadInt32(&stub.chatCalls))

	// Observable state: A now carries a summary and an embedding.
	var (
		sumA *string
		embA *string
	)
	require.NoError(t, database.Pool.QueryRow(ctx,
		`SELECT summary, embedding::text FROM conversation_sessions WHERE id = $1`, sessA).Scan(&sumA, &embA))
	require.NotNil(t, sumA)
	require.Contains(t, *sumA, "Regenerated summary.")
	require.NotNil(t, embA)

	// D was not touched.
	var sumD *string
	require.NoError(t, database.Pool.QueryRow(ctx,
		`SELECT summary FROM conversation_sessions WHERE id = $1`, sessD).Scan(&sumD))
	require.NotNil(t, sumD)
	require.Equal(t, existing, *sumD)

	// A second pass still requests session B (no linked messages) but updates
	// nothing.
	second, err := sessionizer.ResummarizeMissing(ctx, convID, 100)
	require.NoError(t, err)
	require.Equal(t, 1, second.Requested)
	require.Equal(t, 0, second.Updated)
	require.Equal(t, 1, second.Skipped)

	// Disturbance: a canceled context aborts the pass with an error.
	cancelCtx, cancelFn := context.WithCancel(context.Background())
	cancelFn()
	_, err = sessionizer.ResummarizeMissing(cancelCtx, convID, 100)
	require.Error(t, err)
}

// TestSessionizer_ResummarizeMissing_RequiresDependencies covers the guard
// branches for a nil store or nil LLM client. It uses the real database as the
// store for the second case, so no internal dependency is mocked.
func TestSessionizer_ResummarizeMissing_RequiresDependencies(t *testing.T) {
	database := openResummarizeTestDB(t)

	nilStore := pipeline.NewSessionizer(pipeline.SessionizerConfig{})
	_, err := nilStore.ResummarizeMissing(context.Background(), "x", 10)
	require.Error(t, err)

	noLLM := pipeline.NewSessionizer(pipeline.SessionizerConfig{Store: database})
	_, err = noLLM.ResummarizeMissing(context.Background(), "x", 10)
	require.Error(t, err)
}

// TestSessionizer_ResummarizeMissing_Concurrent verifies the bounded worker
// pool updates several sessions and leaves none needing analysis.
//
// [Requirement(PIPE-07)]
func TestSessionizer_ResummarizeMissing_Concurrent(t *testing.T) {
	database := openResummarizeTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	stub := newResummarizeStub(t)
	sessionizer := pipeline.NewSessionizer(pipeline.SessionizerConfig{
		Store:           database,
		LLMClient:       stub.client(),
		SessionGapHours: 2,
		Location:        time.UTC,
		Logger:          zerolog.Nop(),
		Workers:         3,
	})

	convID := fmt.Sprintf("conv-resum-conc-%d", time.Now().UnixNano())
	_, err := database.Pool.Exec(ctx,
		`INSERT INTO conversations (id, name, participants, is_group, is_tracked) VALUES ($1, $2, '[]'::jsonb, false, true)`,
		convID, "Concurrent Resummarize Contact")
	require.NoError(t, err)

	base := time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		start := base.Add(time.Duration(i) * 3 * time.Hour)
		var sessID int64
		require.NoError(t, database.Pool.QueryRow(ctx, `
			INSERT INTO conversation_sessions (conversation_id, started_at, ended_at, message_count, transcript, is_processed)
			VALUES ($1, $2, $3, 1, 'stub transcript', false) RETURNING id`,
			convID, start, start.Add(5*time.Minute)).Scan(&sessID))
		_, err := database.Pool.Exec(ctx, `
			INSERT INTO messages (id, conversation_id, session_id, sender_id, sender_name, is_from_me, sent_at, body)
			VALUES ($1, $2, $3, 'peer', 'Peer', false, $4, 'hi')`,
			fmt.Sprintf("%s-c%d", convID, i), convID, sessID, start.Add(time.Minute))
		require.NoError(t, err)
	}

	report, err := sessionizer.ResummarizeMissing(ctx, convID, 10)
	require.NoError(t, err)
	require.Equal(t, 3, report.Requested)
	require.Equal(t, 3, report.Updated)
	require.Equal(t, 0, report.Failed)
	require.Equal(t, int32(3), atomic.LoadInt32(&stub.chatCalls))

	var remaining int
	require.NoError(t, database.Pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM conversation_sessions
		WHERE conversation_id = $1 AND (summary IS NULL OR summary = '' OR embedding IS NULL)`, convID).Scan(&remaining))
	require.Equal(t, 0, remaining)
}
