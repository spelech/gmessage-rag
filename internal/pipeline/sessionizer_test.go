package pipeline_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/spelech/gmessage-rag/internal/db"
	"github.com/spelech/gmessage-rag/internal/pipeline"
	"github.com/stretchr/testify/require"
)

func openSessionizerTestDB(t *testing.T) *db.DB {
	t.Helper()
	connStr := os.Getenv("DATABASE_URL")
	if connStr == "" {
		connStr = "postgres://jules:postgres@localhost:5432/gmessages?sslmode=disable"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	database, err := db.New(ctx, connStr)
	if err != nil {
		t.Skipf("skipping sessionizer test: database connection failed: %v", err)
	}

	if err := database.Migrate(ctx); err != nil {
		database.Close()
		t.Fatalf("database migration failed: %v", err)
	}

	return database
}

// Contract stub for external third-party LLM boundary (Pillar 1 / Q4).
type mockLLMClient struct {
	mu           sync.Mutex
	summaryCalls int
	embedCalls   int
	title        string
	summary      string
	embedding    []float32
	summaryErr   error
	embedErr     error
}

func (m *mockLLMClient) GenerateSummary(ctx context.Context, transcript string) (string, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.summaryCalls++
	if m.summaryErr != nil {
		return "", "", m.summaryErr
	}
	return m.title, m.summary, nil
}

func (m *mockLLMClient) GenerateEmbedding(ctx context.Context, text string) ([]float32, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.embedCalls++
	if m.embedErr != nil {
		return nil, m.embedErr
	}
	return m.embedding, nil
}

// [Requirement(PIPE-06)] [Requirement(DB-03)]
func TestSessionizer_GroupsByTwoHourGap(t *testing.T) {
	database := openSessionizerTestDB(t)
	defer database.Close()

	ctx := context.Background()
	convID := fmt.Sprintf("conv-sessionizer-%d", time.Now().UnixNano())

	// Insert test conversation into real PostgreSQL
	_, err := database.Pool.Exec(ctx, `
		INSERT INTO conversations (id, name, participants, is_group, is_tracked)
		VALUES ($1, $2, '["+15551234567"]'::jsonb, false, true)
	`, convID, "Sessionizer Test Contact")
	require.NoError(t, err)

	t0 := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)

	// Messages:
	// Msg 1: 10:00
	// Msg 2: 10:45 (gap: 45m <= 2h)
	// Msg 3: 11:30 (gap: 45m <= 2h)
	// --- boundary gap: 2h 30m > 2h ---
	// Msg 4: 14:00
	// Msg 5: 14:15 (gap: 15m <= 2h)
	// --- boundary gap: 4h > 2h ---
	// Msg 6: 18:15
	rawMsgs := []*db.Message{
		{
			ID:             fmt.Sprintf("%s-msg-1", convID),
			ConversationID: convID,
			SenderID:       "alice",
			SenderName:     "Alice",
			SentAt:         t0,
			Body:           "Hello!",
		},
		{
			ID:             fmt.Sprintf("%s-msg-3", convID),
			ConversationID: convID,
			SenderID:       "alice",
			SenderName:     "Alice",
			SentAt:         t0.Add(90 * time.Minute),
			Body:           "Sounds great",
		},
		{
			ID:             fmt.Sprintf("%s-msg-2", convID), // unordered to test sort
			ConversationID: convID,
			SenderID:       "me",
			SenderName:     "Me",
			IsFromMe:       true,
			SentAt:         t0.Add(45 * time.Minute),
			Body:           "Coffee at 11?",
		},
		{
			ID:             fmt.Sprintf("%s-msg-4", convID),
			ConversationID: convID,
			SenderID:       "alice",
			SenderName:     "Alice",
			SentAt:         t0.Add(4 * time.Hour), // 14:00 (gap from 11:30 is 2.5h)
			Body:           "Back from lunch",
		},
		{
			ID:             fmt.Sprintf("%s-msg-5", convID),
			ConversationID: convID,
			SenderID:       "me",
			SenderName:     "Me",
			IsFromMe:       true,
			SentAt:         t0.Add(4*time.Hour + 15*time.Minute),
			Body:           "Nice",
			Reactions: []db.Reaction{
				{Sender: "alice", Emoji: "👍"},
			},
		},
		{
			ID:             fmt.Sprintf("%s-msg-6", convID),
			ConversationID: convID,
			SenderID:       "alice",
			SenderName:     "Alice",
			SentAt:         t0.Add(8*time.Hour + 15*time.Minute), // 18:15 (gap from 14:15 is 4h)
			Body:           "Good evening",
		},
	}

	_, err = database.UpsertMessagesBatch(ctx, rawMsgs)
	require.NoError(t, err)

	unitVec := make([]float32, 1536)
	unitVec[0] = 1.0

	mockLLM := &mockLLMClient{
		title:     "Coffee & Catchup",
		summary:   "Met for coffee in the morning and caught up later.",
		embedding: unitVec,
	}

	sessionizer := pipeline.NewSessionizer(pipeline.SessionizerConfig{
		Store:           database,
		LLMClient:       mockLLM,
		SessionGapHours: 2,
		Location:        time.UTC,
		Logger:          zerolog.Nop(),
	})

	sessions, err := sessionizer.ProcessUnassignedMessages(ctx, convID)
	require.NoError(t, err)

	// Should produce exactly 3 sessions:
	// Session 1: msgs 1, 2, 3 (3 msgs)
	// Session 2: msgs 4, 5 (2 msgs)
	// Session 3: msg 6 (1 msg)
	require.Len(t, sessions, 3, "expected 3 sessions")

	// Session 1 checks
	s1 := sessions[0]
	if s1.MessageCount != 3 {
		t.Errorf("session 1: expected 3 messages, got %d", s1.MessageCount)
	}
	if !s1.StartedAt.Equal(t0) {
		t.Errorf("session 1: expected startedAt %v, got %v", t0, s1.StartedAt)
	}
	if !s1.EndedAt.Equal(t0.Add(90 * time.Minute)) {
		t.Errorf("session 1: expected endedAt %v, got %v", t0.Add(90*time.Minute), s1.EndedAt)
	}
	if s1.Summary == nil || *s1.Summary != "Coffee & Catchup: Met for coffee in the morning and caught up later." {
		t.Errorf("session 1: unexpected summary %v", s1.Summary)
	}
	if s1.Embedding == nil {
		t.Errorf("session 1: expected non-nil embedding")
	}

	// Verify message IDs linked to session 1 in real database
	var linkedS1 []string
	rows1, err := database.Pool.Query(ctx, "SELECT id FROM messages WHERE session_id = $1 ORDER BY sent_at ASC", s1.ID)
	require.NoError(t, err)
	defer rows1.Close()
	for rows1.Next() {
		var id string
		require.NoError(t, rows1.Scan(&id))
		linkedS1 = append(linkedS1, id)
	}
	expectedS1 := []string{fmt.Sprintf("%s-msg-1", convID), fmt.Sprintf("%s-msg-2", convID), fmt.Sprintf("%s-msg-3", convID)}
	require.Equal(t, expectedS1, linkedS1)

	// Session 2 checks
	s2 := sessions[1]
	if s2.MessageCount != 2 {
		t.Errorf("session 2: expected 2 messages, got %d", s2.MessageCount)
	}
	if !s2.StartedAt.Equal(t0.Add(4 * time.Hour)) {
		t.Errorf("session 2: expected startedAt %v, got %v", t0.Add(4*time.Hour), s2.StartedAt)
	}
	var linkedS2 []string
	rows2, err := database.Pool.Query(ctx, "SELECT id FROM messages WHERE session_id = $1 ORDER BY sent_at ASC", s2.ID)
	require.NoError(t, err)
	defer rows2.Close()
	for rows2.Next() {
		var id string
		require.NoError(t, rows2.Scan(&id))
		linkedS2 = append(linkedS2, id)
	}
	expectedS2 := []string{fmt.Sprintf("%s-msg-4", convID), fmt.Sprintf("%s-msg-5", convID)}
	require.Equal(t, expectedS2, linkedS2)

	// Session 3 checks
	s3 := sessions[2]
	if s3.MessageCount != 1 {
		t.Errorf("session 3: expected 1 message, got %d", s3.MessageCount)
	}
	if !s3.StartedAt.Equal(t0.Add(8*time.Hour + 15*time.Minute)) {
		t.Errorf("session 3: expected startedAt %v, got %v", t0.Add(8*time.Hour+15*time.Minute), s3.StartedAt)
	}

	// Verify transcript format with reactions
	if s2.Transcript == "" || !containsSubstr(s2.Transcript, "[👍]") {
		t.Errorf("expected reaction emoji in session 2 transcript: %q", s2.Transcript)
	}
}

func containsSubstr(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 || (len(s) > 0 && len(substr) > 0 && findSubstr(s, substr)))
}

func findSubstr(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestSessionizer_BoundariesAndEdgeCases(t *testing.T) {
	database := openSessionizerTestDB(t)
	defer database.Close()

	sessionizer := pipeline.NewSessionizer(pipeline.SessionizerConfig{
		Store:  database,
		Logger: zerolog.Nop(),
	})

	ctx := context.Background()
	convEmpty := fmt.Sprintf("conv-empty-%d", time.Now().UnixNano())

	// Insert empty conversation
	_, err := database.Pool.Exec(ctx, `
		INSERT INTO conversations (id, name, participants, is_group, is_tracked)
		VALUES ($1, $2, '["+15551234567"]'::jsonb, false, true)
	`, convEmpty, "Empty Conversation")
	require.NoError(t, err)

	// 0 Messages: empty result
	sessions, err := sessionizer.ProcessUnassignedMessages(ctx, convEmpty)
	require.NoError(t, err)
	if len(sessions) != 0 {
		t.Errorf("expected 0 sessions for empty conversation, got %d", len(sessions))
	}

	// Empty conversation ID validation
	_, err = sessionizer.ProcessUnassignedMessages(ctx, "")
	if err == nil {
		t.Error("expected error for empty conversationID, got nil")
	}

	// Nil store validation
	nilStoreSessionizer := pipeline.NewSessionizer(pipeline.SessionizerConfig{})
	_, err = nilStoreSessionizer.ProcessUnassignedMessages(ctx, "conv-1")
	if err == nil {
		t.Error("expected error for nil store, got nil")
	}

	// 1 Message: exactly 1 session
	convSingle := fmt.Sprintf("conv-single-%d", time.Now().UnixNano())
	_, err = database.Pool.Exec(ctx, `
		INSERT INTO conversations (id, name, participants, is_group, is_tracked)
		VALUES ($1, $2, '["+15551234567"]'::jsonb, false, true)
	`, convSingle, "Single Message Conversation")
	require.NoError(t, err)

	t0 := time.Now().UTC()
	singleMsgID := fmt.Sprintf("%s-m1", convSingle)
	err = database.UpsertMessage(ctx, &db.Message{
		ID:             singleMsgID,
		ConversationID: convSingle,
		SenderID:       "single-sender",
		SenderName:     "Single",
		SentAt:         t0,
		Body:           "Lone message",
	})
	require.NoError(t, err)

	singleSess, err := sessionizer.ProcessUnassignedMessages(ctx, convSingle)
	require.NoError(t, err)
	if len(singleSess) != 1 || singleSess[0].MessageCount != 1 {
		t.Fatalf("expected 1 session with count 1, got %v", singleSess)
	}

	// Context cancellation
	cancelCtx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = sessionizer.ProcessUnassignedMessages(cancelCtx, convSingle)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled error, got %v", err)
	}

	// Closed DB pool error propagation
	closedDB, err := db.New(context.Background(), database.Pool.Config().ConnString())
	if err == nil {
		closedDB.Close()
		closedSessionizer := pipeline.NewSessionizer(pipeline.SessionizerConfig{
			Store:  closedDB,
			Logger: zerolog.Nop(),
		})
		_, err = closedSessionizer.ProcessUnassignedMessages(ctx, convSingle)
		if err == nil {
			t.Error("expected closed db error propagation, got nil")
		}
	}
}

func TestSessionizer_LLMFailureDegradation(t *testing.T) {
	database := openSessionizerTestDB(t)
	defer database.Close()

	ctx := context.Background()
	convID := fmt.Sprintf("conv-llm-fail-%d", time.Now().UnixNano())

	_, err := database.Pool.Exec(ctx, `
		INSERT INTO conversations (id, name, participants, is_group, is_tracked)
		VALUES ($1, $2, '["+15551234567"]'::jsonb, false, true)
	`, convID, "LLM Fail Conversation")
	require.NoError(t, err)

	t0 := time.Now().UTC()
	err = database.UpsertMessage(ctx, &db.Message{
		ID:             fmt.Sprintf("%s-m-fail", convID),
		ConversationID: convID,
		SenderID:       "tester",
		SenderName:     "Tester",
		SentAt:         t0,
		Body:           "Testing failure",
	})
	require.NoError(t, err)

	failingLLM := &mockLLMClient{
		summaryErr: errors.New("rate limited"),
		embedErr:   errors.New("timeout"),
	}

	sessionizer := pipeline.NewSessionizer(pipeline.SessionizerConfig{
		Store:     database,
		LLMClient: failingLLM,
		Logger:    zerolog.Nop(),
	})

	// Process should still succeed by saving the session with transcript even if summary/embed fails
	sessions, err := sessionizer.ProcessUnassignedMessages(context.Background(), convID)
	require.NoError(t, err, "expected graceful degradation on LLM failure")
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	if sessions[0].Transcript == "" {
		t.Errorf("expected transcript to be populated")
	}
	if sessions[0].Summary != nil {
		t.Errorf("expected nil summary on LLM failure, got %v", sessions[0].Summary)
	}
	if sessions[0].Embedding != nil {
		t.Errorf("expected nil embedding on LLM failure, got %v", sessions[0].Embedding)
	}
}
