package pipeline_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/spelech/gmessage-rag/internal/db"
	"github.com/spelech/gmessage-rag/internal/pipeline"
)

type mockSessionStore struct {
	mu           sync.Mutex
	unassigned   map[string][]db.Message
	sessions     []*db.ConversationSession
	linkedMsgIDs map[int64][]string
	upsertErr    error
	getErr       error
}

func newMockSessionStore() *mockSessionStore {
	return &mockSessionStore{
		unassigned:   make(map[string][]db.Message),
		linkedMsgIDs: make(map[int64][]string),
	}
}

func (m *mockSessionStore) GetUnassignedMessages(ctx context.Context, conversationID string) ([]db.Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.getErr != nil {
		return nil, m.getErr
	}
	return m.unassigned[conversationID], nil
}

func (m *mockSessionStore) UpsertSession(ctx context.Context, session *db.ConversationSession, messageIDs []string) (*db.ConversationSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.upsertErr != nil {
		return nil, m.upsertErr
	}

	sessionCopy := *session
	sessionCopy.ID = int64(len(m.sessions) + 1)
	sessionCopy.CreatedAt = time.Now()
	sessionCopy.UpdatedAt = time.Now()

	m.sessions = append(m.sessions, &sessionCopy)
	m.linkedMsgIDs[sessionCopy.ID] = messageIDs
	return &sessionCopy, nil
}

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
	rawMsgs := []db.Message{
		{
			ID:             "msg-1",
			ConversationID: "conv-1",
			SenderID:       "alice",
			SenderName:     "Alice",
			SentAt:         t0,
			Body:           "Hello!",
		},
		{
			ID:             "msg-3",
			ConversationID: "conv-1",
			SenderID:       "alice",
			SenderName:     "Alice",
			SentAt:         t0.Add(90 * time.Minute),
			Body:           "Sounds great",
		},
		{
			ID:             "msg-2", // unordered to test sort
			ConversationID: "conv-1",
			SenderID:       "me",
			SenderName:     "Me",
			IsFromMe:       true,
			SentAt:         t0.Add(45 * time.Minute),
			Body:           "Coffee at 11?",
		},
		{
			ID:             "msg-4",
			ConversationID: "conv-1",
			SenderID:       "alice",
			SenderName:     "Alice",
			SentAt:         t0.Add(4 * time.Hour), // 14:00 (gap from 11:30 is 2.5h)
			Body:           "Back from lunch",
		},
		{
			ID:             "msg-5",
			ConversationID: "conv-1",
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
			ID:             "msg-6",
			ConversationID: "conv-1",
			SenderID:       "alice",
			SenderName:     "Alice",
			SentAt:         t0.Add(8*time.Hour + 15*time.Minute), // 18:15 (gap from 14:15 is 4h)
			Body:           "Good evening",
		},
	}

	store := newMockSessionStore()
	store.unassigned["conv-1"] = rawMsgs

	unitVec := make([]float32, 1536)
	unitVec[0] = 1.0

	mockLLM := &mockLLMClient{
		title:     "Coffee & Catchup",
		summary:   "Met for coffee in the morning and caught up later.",
		embedding: unitVec,
	}

	sessionizer := pipeline.NewSessionizer(pipeline.SessionizerConfig{
		Store:           store,
		LLMClient:       mockLLM,
		SessionGapHours: 2,
		Location:        time.UTC,
		Logger:          zerolog.Nop(),
	})

	ctx := context.Background()
	sessions, err := sessionizer.ProcessUnassignedMessages(ctx, "conv-1")
	if err != nil {
		t.Fatalf("ProcessUnassignedMessages failed: %v", err)
	}

	// Should produce exactly 3 sessions:
	// Session 1: msgs 1, 2, 3 (3 msgs)
	// Session 2: msgs 4, 5 (2 msgs)
	// Session 3: msg 6 (1 msg)
	if len(sessions) != 3 {
		t.Fatalf("expected 3 sessions, got %d", len(sessions))
	}

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

	// Verify message IDs linked to session 1
	linked1 := store.linkedMsgIDs[s1.ID]
	if len(linked1) != 3 || linked1[0] != "msg-1" || linked1[1] != "msg-2" || linked1[2] != "msg-3" {
		t.Errorf("session 1: unexpected linked msg IDs: %v", linked1)
	}

	// Session 2 checks
	s2 := sessions[1]
	if s2.MessageCount != 2 {
		t.Errorf("session 2: expected 2 messages, got %d", s2.MessageCount)
	}
	if !s2.StartedAt.Equal(t0.Add(4 * time.Hour)) {
		t.Errorf("session 2: expected startedAt %v, got %v", t0.Add(4*time.Hour), s2.StartedAt)
	}
	linked2 := store.linkedMsgIDs[s2.ID]
	if len(linked2) != 2 || linked2[0] != "msg-4" || linked2[1] != "msg-5" {
		t.Errorf("session 2: unexpected linked msg IDs: %v", linked2)
	}

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
	store := newMockSessionStore()
	sessionizer := pipeline.NewSessionizer(pipeline.SessionizerConfig{
		Store:  store,
		Logger: zerolog.Nop(),
	})

	ctx := context.Background()

	// 0 Messages: empty result
	sessions, err := sessionizer.ProcessUnassignedMessages(ctx, "conv-empty")
	if err != nil {
		t.Fatalf("expected nil error for empty conversation, got %v", err)
	}
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
	t0 := time.Now()
	store.unassigned["conv-single"] = []db.Message{
		{ID: "m-1", ConversationID: "conv-single", SentAt: t0, Body: "Lone message"},
	}
	singleSess, err := sessionizer.ProcessUnassignedMessages(ctx, "conv-single")
	if err != nil {
		t.Fatalf("single message process failed: %v", err)
	}
	if len(singleSess) != 1 || singleSess[0].MessageCount != 1 {
		t.Fatalf("expected 1 session with count 1, got %v", singleSess)
	}

	// Context cancellation
	cancelCtx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = sessionizer.ProcessUnassignedMessages(cancelCtx, "conv-single")
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled error, got %v", err)
	}

	// Store errors propagate cleanly
	store.getErr = errors.New("db connection lost")
	_, err = sessionizer.ProcessUnassignedMessages(ctx, "conv-single")
	if err == nil {
		t.Error("expected store error propagation, got nil")
	}
	store.getErr = nil

	store.upsertErr = errors.New("db write failed")
	_, err = sessionizer.ProcessUnassignedMessages(ctx, "conv-single")
	if err == nil {
		t.Error("expected upsert error propagation, got nil")
	}
}

func TestSessionizer_LLMFailureDegradation(t *testing.T) {
	store := newMockSessionStore()
	t0 := time.Now()
	store.unassigned["conv-llm-fail"] = []db.Message{
		{ID: "m-fail", ConversationID: "conv-llm-fail", SentAt: t0, Body: "Testing failure"},
	}

	failingLLM := &mockLLMClient{
		summaryErr: errors.New("rate limited"),
		embedErr:   errors.New("timeout"),
	}

	sessionizer := pipeline.NewSessionizer(pipeline.SessionizerConfig{
		Store:     store,
		LLMClient: failingLLM,
		Logger:    zerolog.Nop(),
	})

	// Process should still succeed by saving the session with transcript even if summary/embed fails
	sessions, err := sessionizer.ProcessUnassignedMessages(context.Background(), "conv-llm-fail")
	if err != nil {
		t.Fatalf("expected graceful degradation on LLM failure, got %v", err)
	}
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
