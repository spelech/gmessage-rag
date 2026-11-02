package db_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/pgvector/pgvector-go"
	"github.com/spelech/gmessage-rag/internal/db"
)

func getTestDB(t *testing.T) *db.DB {
	t.Helper()
	connStr := os.Getenv("DATABASE_URL")
	if connStr == "" {
		connStr = "postgres://jules:postgres@localhost:5432/gmessages?sslmode=disable"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	database, err := db.New(ctx, connStr)
	if err != nil {
		t.Skipf("skipping test: database connection failed: %v", err)
	}

	if err := database.Migrate(ctx); err != nil {
		database.Close()
		t.Fatalf("database migration failed: %v", err)
	}

	return database
}

// Helper to make normalized 1536-dimensional unit vector along index axis.
func makeUnitVec(dimIndex int) []float32 {
	v := make([]float32, 1536)
	if dimIndex >= 0 && dimIndex < 1536 {
		v[dimIndex] = 1.0
	}
	return v
}

// [Requirement(DB-03)]
func TestSessionGrouping_2HourGap(t *testing.T) {
	database := getTestDB(t)
	defer database.Close()

	ctx := context.Background()
	convID := fmt.Sprintf("conv-test-gap-%d", time.Now().UnixNano())

	// Insert test conversation
	_, err := database.Pool.Exec(ctx, `
		INSERT INTO conversations (id, name, participants, is_group, is_tracked)
		VALUES ($1, $2, '["+15551234567"]'::jsonb, false, true)
	`, convID, "Test Contact")
	if err != nil {
		t.Fatalf("insert conversation failed: %v", err)
	}

	baseTime := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)

	// Insert raw messages
	msgIDs := []string{
		fmt.Sprintf("msg-1-%d", time.Now().UnixNano()),
		fmt.Sprintf("msg-2-%d", time.Now().UnixNano()),
		fmt.Sprintf("msg-3-%d", time.Now().UnixNano()),
	}

	for i, id := range msgIDs {
		msgTime := baseTime.Add(time.Duration(i*30) * time.Minute) // 10:00, 10:30, 11:00 (within 2 hours)
		_, err := database.Pool.Exec(ctx, `
			INSERT INTO messages (id, conversation_id, sender_id, sender_name, is_from_me, sent_at, body)
			VALUES ($1, $2, 'sender-1', 'Sender', false, $3, $4)
		`, id, convID, msgTime, fmt.Sprintf("Message body %d", i+1))
		if err != nil {
			t.Fatalf("insert message %s failed: %v", id, err)
		}
	}

	summaryText := "Session summary discussing test plan"
	transcriptText := "[2026-11-01 10:00:00] Sender: Message body 1\n[2026-11-01 10:30:00] Sender: Message body 2\n[2026-11-01 11:00:00] Sender: Message body 3"
	hv := pgvector.NewHalfVector(makeUnitVec(0))

	session := &db.ConversationSession{
		ConversationID: convID,
		StartedAt:      baseTime,
		EndedAt:        baseTime.Add(1 * time.Hour),
		MessageCount:   3,
		Summary:        &summaryText,
		Transcript:     transcriptText,
		Embedding:      &hv,
		IsProcessed:    true,
	}

	// Upsert session and link raw messages
	savedSession, err := database.UpsertSession(ctx, session, msgIDs)
	if err != nil {
		t.Fatalf("UpsertSession failed: %v", err)
	}

	if savedSession.ID == 0 {
		t.Fatalf("expected non-zero session ID, got 0")
	}

	// Verify messages are linked to session_id
	rows, err := database.Pool.Query(ctx, `
		SELECT id, session_id FROM messages WHERE conversation_id = $1
	`, convID)
	if err != nil {
		t.Fatalf("query messages failed: %v", err)
	}
	defer rows.Close()

	linkedCount := 0
	for rows.Next() {
		var mID string
		var sID *int64
		if err := rows.Scan(&mID, &sID); err != nil {
			t.Fatalf("scan message failed: %v", err)
		}
		if sID == nil || *sID != savedSession.ID {
			t.Fatalf("message %s not linked to session %d (got %v)", mID, savedSession.ID, sID)
		}
		linkedCount++
	}

	if linkedCount != len(msgIDs) {
		t.Fatalf("expected %d linked messages, got %d", len(msgIDs), linkedCount)
	}

	// Test idempotency and updates on conflict
	updatedSummary := "Updated session summary"
	savedSession.Summary = &updatedSummary
	savedSession.MessageCount = 4
	updatedSession, err := database.UpsertSession(ctx, savedSession, nil)
	if err != nil {
		t.Fatalf("idempotent UpsertSession failed: %v", err)
	}
	if updatedSession.ID != savedSession.ID {
		t.Fatalf("expected same session ID %d, got %d", savedSession.ID, updatedSession.ID)
	}

	var fetchedSummary string
	var fetchedCount int
	err = database.Pool.QueryRow(ctx, `
		SELECT summary, message_count FROM conversation_sessions WHERE id = $1
	`, updatedSession.ID).Scan(&fetchedSummary, &fetchedCount)
	if err != nil {
		t.Fatalf("query updated session failed: %v", err)
	}
	if fetchedSummary != updatedSummary || fetchedCount != 4 {
		t.Fatalf("expected summary %q and count 4, got %q and %d", updatedSummary, fetchedSummary, fetchedCount)
	}
}

// [Requirement(DB-04)]
func TestSearchSessionsHybrid_RRF(t *testing.T) {
	database := getTestDB(t)
	defer database.Close()

	ctx := context.Background()
	convID := fmt.Sprintf("conv-test-rrf-%d", time.Now().UnixNano())

	// Insert test conversation
	_, err := database.Pool.Exec(ctx, `
		INSERT INTO conversations (id, name, participants, is_group, is_tracked)
		VALUES ($1, $2, '["+15559876543"]'::jsonb, false, true)
	`, convID, "RRF Test Contact")
	if err != nil {
		t.Fatalf("insert conversation failed: %v", err)
	}

	t0 := time.Date(2026, 11, 2, 8, 0, 0, 0, time.UTC)

	// Vectors
	vecTarget := makeUnitVec(10)
	vecOrthogonal := makeUnitVec(20)

	sumA := "Discussion regarding secret quantum cryptography protocols"
	transA := "Alice and Bob explored novel quantum key distribution."
	hvOrth := pgvector.NewHalfVector(vecOrthogonal)
	sessionA := &db.ConversationSession{
		ConversationID: convID,
		StartedAt:      t0,
		EndedAt:        t0.Add(1 * time.Hour),
		MessageCount:   2,
		Summary:        &sumA,
		Transcript:     transA,
		Embedding:      &hvOrth, // keyword match, vector distant
		IsProcessed:    true,
	}

	sumB := "Discussion about routine weather forecast"
	transB := "It looks like light rain in the afternoon."
	hvTarget := pgvector.NewHalfVector(vecTarget)
	sessionB := &db.ConversationSession{
		ConversationID: convID,
		StartedAt:      t0.Add(3 * time.Hour),
		EndedAt:        t0.Add(4 * time.Hour),
		MessageCount:   2,
		Summary:        &sumB,
		Transcript:     transB,
		Embedding:      &hvTarget, // vector exact match, text mismatch
		IsProcessed:    true,
	}

	sumC := "Quantum cryptography security review and keys"
	transC := "Deep analysis of quantum cryptography resilience."
	sessionC := &db.ConversationSession{
		ConversationID: convID,
		StartedAt:      t0.Add(6 * time.Hour),
		EndedAt:        t0.Add(7 * time.Hour),
		MessageCount:   2,
		Summary:        &sumC,
		Transcript:     transC,
		Embedding:      &hvTarget, // blended: keyword match AND vector exact match
		IsProcessed:    true,
	}

	sumD := "Quantum cryptography archived historical record"
	transD := "Old historical quantum session."
	sessionD := &db.ConversationSession{
		ConversationID: convID,
		StartedAt:      t0.Add(24 * time.Hour),
		EndedAt:        t0.Add(25 * time.Hour),
		MessageCount:   1,
		Summary:        &sumD,
		Transcript:     transD,
		Embedding:      &hvTarget, // outside target time window
		IsProcessed:    true,
	}

	sA, err := database.UpsertSession(ctx, sessionA, nil)
	if err != nil {
		t.Fatalf("upsert sessionA failed: %v", err)
	}
	sB, err := database.UpsertSession(ctx, sessionB, nil)
	if err != nil {
		t.Fatalf("upsert sessionB failed: %v", err)
	}
	sC, err := database.UpsertSession(ctx, sessionC, nil)
	if err != nil {
		t.Fatalf("upsert sessionC failed: %v", err)
	}
	sD, err := database.UpsertSession(ctx, sessionD, nil)
	if err != nil {
		t.Fatalf("upsert sessionD failed: %v", err)
	}

	// 1. Pure Keyword Match
	// Search for "cryptography" with irrelevant vector
	irrVec := makeUnitVec(99)
	resultsKeyword, err := database.SearchSessionsHybrid(ctx, convID, "cryptography", irrVec, nil, nil, 10)
	if err != nil {
		t.Fatalf("pure keyword SearchSessionsHybrid failed: %v", err)
	}
	if len(resultsKeyword) == 0 {
		t.Fatalf("expected keyword results, got 0")
	}
	// Session B has no mention of cryptography, so in FTS matches it won't appear, or only via vector
	foundA := false
	for _, r := range resultsKeyword {
		if r.ID == sA.ID {
			foundA = true
		}
	}
	if !foundA {
		t.Errorf("expected session A in keyword search results")
	}

	// 2. Pure Vector Match
	// Search with nonsensical text that doesn't match any session
	resultsVector, err := database.SearchSessionsHybrid(ctx, convID, "xyznonexistentqueryxyz", vecTarget, nil, nil, 10)
	if err != nil {
		t.Fatalf("pure vector SearchSessionsHybrid failed: %v", err)
	}
	if len(resultsVector) == 0 {
		t.Fatalf("expected vector results, got 0")
	}
	// Target matches sB, sC, sD closely. B, C, and D share the exact target vector,
	// so their vector ranks are a tie and any of the three may rank first.
	topVecResult := resultsVector[0]
	if topVecResult.ID == sA.ID {
		t.Errorf("session A should not be top vector match")
	}
	if topVecResult.ID != sB.ID && topVecResult.ID != sC.ID && topVecResult.ID != sD.ID {
		t.Errorf("expected top vector match to be one of B (%d), C (%d), D (%d), got %d", sB.ID, sC.ID, sD.ID, topVecResult.ID)
	}

	// 3. Blended Hybrid Match
	// Search with "cryptography" and vecTarget. Session C should have the highest combined RRF score.
	// Expected RRF formula: 1/(60+rank_fts) + 1/(60+rank_vec).
	resultsBlended, err := database.SearchSessionsHybrid(ctx, convID, "cryptography", vecTarget, nil, nil, 10)
	if err != nil {
		t.Fatalf("blended SearchSessionsHybrid failed: %v", err)
	}
	if len(resultsBlended) == 0 {
		t.Fatalf("expected blended results, got 0")
	}

	// Session C is both top FTS candidate and top vector candidate. It must be #1 ranked.
	if resultsBlended[0].ID != sC.ID {
		t.Fatalf("expected Session C (ID %d) to be ranked #1, got ID %d (score: %f)", sC.ID, resultsBlended[0].ID, resultsBlended[0].RRFScore)
	}

	// Verify RRF score computation: score should be > 1/(60+1) since both FTS and Vector matched
	expectedMinScore := 1.0 / 61.0
	if resultsBlended[0].RRFScore <= expectedMinScore {
		t.Errorf("expected blended RRF score > %f, got %f", expectedMinScore, resultsBlended[0].RRFScore)
	}

	// 4. Temporal Filter Bounds (dateFrom, dateTo)
	dateFrom := t0.Add(-1 * time.Hour)
	dateTo := t0.Add(8 * time.Hour) // Excludes session D which started at t0 + 24h
	resultsFiltered, err := database.SearchSessionsHybrid(ctx, convID, "cryptography", vecTarget, &dateFrom, &dateTo, 10)
	if err != nil {
		t.Fatalf("temporal filtered SearchSessionsHybrid failed: %v", err)
	}

	for _, r := range resultsFiltered {
		if r.ID == sD.ID {
			t.Fatalf("session D (started at %v) should have been filtered out by dateTo (%v)", r.StartedAt, dateTo)
		}
		if r.StartedAt.Before(dateFrom) || r.EndedAt.After(dateTo) {
			t.Fatalf("session %d out of bounds [%v, %v]: started %v, ended %v", r.ID, dateFrom, dateTo, r.StartedAt, r.EndedAt)
		}
	}

	// Verify ranking order is monotonically decreasing
	for i := 1; i < len(resultsFiltered); i++ {
		if resultsFiltered[i].RRFScore > resultsFiltered[i-1].RRFScore+1e-9 {
			t.Errorf("results not ordered by rrf_score desc: idx %d (%f) > idx %d (%f)",
				i, resultsFiltered[i].RRFScore, i-1, resultsFiltered[i-1].RRFScore)
		}
	}

	// 0-1-N boundary check: limit = 1
	resultsLimit1, err := database.SearchSessionsHybrid(ctx, convID, "cryptography", vecTarget, nil, nil, 1)
	if err != nil {
		t.Fatalf("limit 1 SearchSessionsHybrid failed: %v", err)
	}
	if len(resultsLimit1) != 1 {
		t.Fatalf("expected exactly 1 result with limit=1, got %d", len(resultsLimit1))
	}
	if resultsLimit1[0].ID != sC.ID {
		t.Errorf("expected limit=1 result to be Session C, got ID %d", resultsLimit1[0].ID)
	}
}

// TestSearchSessionsHybridAllConversations covers the cross-conversation RRF query,
// consumed by the search_conversation_history MCP tool (MCP-03).
func TestSearchSessionsHybridAllConversations(t *testing.T) {
	database := getTestDB(t)
	defer database.Close()

	ctx := context.Background()
	tag := time.Now().UnixNano()
	convA := fmt.Sprintf("conv-rrf-all-a-%d", tag)
	convB := fmt.Sprintf("conv-rrf-all-b-%d", tag)

	for _, c := range []string{convA, convB} {
		if _, err := database.Pool.Exec(ctx, `
			INSERT INTO conversations (id, name, participants, is_group, is_tracked)
			VALUES ($1, $2, '["+15550002222"]'::jsonb, false, true)
		`, c, "RRF All Test"); err != nil {
			t.Fatalf("insert conversation %s failed: %v", c, err)
		}
	}

	t0 := time.Date(2026, 11, 2, 8, 0, 0, 0, time.UTC)

	// A unique token keeps the FTS leg isolated from rows left by earlier runs.
	token := fmt.Sprintf("zebratoken%d", tag)

	sumA := "Migration planning " + token
	transA := "Discussed migration corridors for " + token + "."
	hvA := pgvector.NewHalfVector(makeUnitVec(10))
	if _, err := database.UpsertSession(ctx, &db.ConversationSession{
		ConversationID: convA, StartedAt: t0, EndedAt: t0.Add(time.Hour),
		MessageCount: 1, Summary: &sumA, Transcript: transA, Embedding: &hvA, IsProcessed: true,
	}, nil); err != nil {
		t.Fatalf("upsert session A failed: %v", err)
	}

	sumB := "Conservation fundraising " + token
	transB := "Planned conservation funding for " + token + "."
	hvB := pgvector.NewHalfVector(makeUnitVec(11))
	if _, err := database.UpsertSession(ctx, &db.ConversationSession{
		ConversationID: convB, StartedAt: t0.Add(2 * time.Hour), EndedAt: t0.Add(3 * time.Hour),
		MessageCount: 1, Summary: &sumB, Transcript: transB, Embedding: &hvB, IsProcessed: true,
	}, nil); err != nil {
		t.Fatalf("upsert session B failed: %v", err)
	}

	results, err := database.SearchSessionsHybridAllConversations(ctx, token, nil, nil, nil, 100)
	if err != nil {
		t.Fatalf("SearchSessionsHybridAllConversations failed: %v", err)
	}

	found := map[string]bool{}
	for _, r := range results {
		if r.ConversationID == convA || r.ConversationID == convB {
			found[r.ConversationID] = true
		}
	}
	if !found[convA] || !found[convB] {
		t.Fatalf("expected sessions from both conversations, got found=%v (n=%d)", found, len(results))
	}
}
