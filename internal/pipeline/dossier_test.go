package pipeline_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"

	"github.com/spelech/gmessage-rag/internal/db"
	"github.com/spelech/gmessage-rag/internal/pipeline"
)

// openPipelineTestDB connects to a real Postgres+pgvector database, applies migrations, and skips
// cleanly when the database is unreachable.
func openPipelineTestDB(t *testing.T) *db.DB {
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
	t.Cleanup(database.Close)

	if err := database.Migrate(ctx); err != nil {
		t.Fatalf("database migration failed: %v", err)
	}
	return database
}

// eventList normalizes a structured-state event array into []map[string]any.
func eventList(state map[string]any, key string) []map[string]any {
	raw, ok := state[key].([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(raw))
	for _, e := range raw {
		if m, ok := e.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// findEvent returns the event with the given id from a structured-state array, or nil.
func findEvent(state map[string]any, key, id string) map[string]any {
	for _, e := range eventList(state, key) {
		if e["id"] == id {
			return e
		}
	}
	return nil
}

// requireEvent returns the event with the given id, failing the test when absent.
func requireEvent(t *testing.T, state map[string]any, key, id string) map[string]any {
	t.Helper()
	ev := findEvent(state, key, id)
	require.NotNil(t, ev, "expected %s[%s] in structured state", key, id)
	return ev
}

// toStringSlice normalizes a JSON-decoded string array to []string.
func toStringSlice(v any) []string {
	raw, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, e := range raw {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// TestDossierReconciliation_DateAnchoring drives the Living Dossier reconciler end-to-end against
// real Postgres with only the external LLM HTTP boundary stubbed. It asserts the persisted dossier
// row, the appended audit history, and the exported Markdown.
//
// [Requirement(PIPE-03)]
func TestDossierReconciliation_DateAnchoring(t *testing.T) {
	database := openPipelineTestDB(t)
	ctx := context.Background()

	convID := fmt.Sprintf("conv-dossier-anchor-%d", time.Now().UnixNano())
	require.NoError(t, database.UpsertConversation(ctx, &db.Conversation{
		ID:           convID,
		Name:         "Jane Doe",
		Participants: []string{"+15551234567"},
		IsTracked:    true,
	}))

	base := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	summary := "Jane confirms a dentist visit for the next day."
	session, err := database.UpsertSession(ctx, &db.ConversationSession{
		ConversationID: convID,
		StartedAt:      base,
		EndedAt:        base.Add(2 * time.Hour),
		MessageCount:   2,
		Summary:        &summary,
		Transcript:     "[2026-11-02 09:00:00] Jane: Dentist tomorrow at 2pm.",
		IsProcessed:    true,
	}, nil)
	require.NoError(t, err)

	// Seed a prior dossier (version 1) holding a stale event that the new session supersedes.
	priorProcessed := base.Add(-24 * time.Hour)
	_, err = database.UpsertContactDossier(ctx, convID, "Jane Doe",
		"# Living Dossier: Jane Doe\n\nOld notes.",
		map[string]any{
			"upcoming_events": []any{
				map[string]any{"id": "stale-1", "title": "Stale item", "date": "2026-01-01"},
			},
		},
		nil, &priorProcessed)
	require.NoError(t, err)

	// Contract stub for the external LLM HTTP boundary: a relative "tomorrow", a superseded stale
	// entry, and a relative "yesterday" that must transition into the past.
	var chatCalls int32
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		atomic.AddInt32(&chatCalls, 1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"index": 0,
				"message": map[string]string{
					"role": "assistant",
					"content": `{
						"markdown": "Jane has a dentist appointment approaching.",
						"structured_state": {
							"profile": {"job": "Engineer"},
							"upcoming_events": [
								{"id": "evt-1", "title": "Dentist appointment", "relative_date": "tomorrow", "details": "2pm"},
								{"id": "stale-1", "title": "Stale item", "date": "2026-01-01"},
								{"id": "evt-old", "title": "Old sync", "relative_date": "yesterday"}
							],
							"open_loops": []
						},
						"supersedes": ["stale-1"]
					}`,
				},
			}},
		})
	}))
	defer stub.Close()

	model := pipeline.NewOpenAIDossierModel(pipeline.DossierModelConfig{
		BaseURL: stub.URL,
		APIKey:  "test-key",
		Model:   "test-dossier",
		Logger:  zerolog.Nop(),
	})

	outDir := t.TempDir()
	reconciler := pipeline.NewDossierReconciler(pipeline.DossierReconcilerConfig{
		Store:     database,
		Model:     model,
		OutputDir: outDir,
		Location:  time.UTC,
		Logger:    zerolog.Nop(),
	})

	outcome, err := reconciler.Reconcile(ctx, convID)
	require.NoError(t, err)
	require.True(t, outcome.Changed)

	// Observable DB state: version advanced (2) and prior state snapshotted into history.
	dossier, err := database.GetContactDossier(ctx, convID)
	require.NoError(t, err)
	require.Equal(t, 2, dossier.Version)

	history, err := database.GetDossierHistory(ctx, convID, 10)
	require.NoError(t, err)
	require.Len(t, history, 1)
	require.Equal(t, 1, history[0].Version)
	require.NotNil(t, history[0].TriggerSessionID)
	require.Equal(t, session.ID, *history[0].TriggerSessionID)

	// Relative "tomorrow" anchored to the newest session date (2026-11-02 -> 2026-11-03).
	upcoming := requireEvent(t, dossier.StructuredState, "upcoming_events", "evt-1")
	require.Equal(t, "2026-11-03", upcoming["date"])

	// Chronological override removed the superseded entry.
	require.Nil(t, findEvent(dossier.StructuredState, "upcoming_events", "stale-1"))

	// "yesterday" anchored and transitioned into past_events.
	past := requireEvent(t, dossier.StructuredState, "past_events", "evt-old")
	require.Equal(t, "2026-11-01", past["date"])
	require.Equal(t, "occurred", past["status"])
	require.Nil(t, findEvent(dossier.StructuredState, "upcoming_events", "evt-old"))

	// Written Markdown reflects the anchored calendar date.
	mdPath := filepath.Join(outDir, "jane-doe.md")
	content, err := os.ReadFile(mdPath)
	require.NoError(t, err)
	require.Contains(t, string(content), "2026-11-03")
	require.Contains(t, string(content), "Dentist appointment")
	require.Equal(t, mdPath, outcome.MarkdownPath)

	// The external LLM boundary was exercised exactly once.
	require.Equal(t, int32(1), atomic.LoadInt32(&chatCalls))
}

// TestAnchorRelativeDates_Boundaries exercises the deterministic date-anchoring and past-event
// transition helpers directly (0-1-N plus weekday and offset branches).
//
// [Requirement(PIPE-03)]
func TestAnchorRelativeDates_Boundaries(t *testing.T) {
	// 2026-11-02 is a Monday.
	base := time.Date(2026, 11, 2, 14, 0, 0, 0, time.UTC)

	cases := []struct {
		phrase string
		want   string
	}{
		{"today", "2026-11-02"},
		{"tomorrow", "2026-11-03"},
		{"yesterday", "2026-11-01"},
		{"next week", "2026-11-09"},
		{"in 3 days", "2026-11-05"},
		{"next monday", "2026-11-09"},
		{"tuesday", "2026-11-03"},
		{"This Friday", "2026-11-06"},
	}

	for _, tc := range cases {
		state := map[string]any{
			"upcoming_events": []any{
				map[string]any{"id": "e1", "title": "Event", "relative_date": tc.phrase},
			},
		}
		pipeline.AnchorRelativeDates(state, base)
		event := findEvent(state, "upcoming_events", "e1")
		require.NotNil(t, event)
		require.Equal(t, tc.want, event["date"], "phrase %q", tc.phrase)
	}

	// An unrecognized phrase must not fabricate a date.
	unknown := map[string]any{
		"open_loops": []any{
			map[string]any{"id": "loop-1", "title": "Someday", "relative_date": "whenever later"},
		},
	}
	pipeline.AnchorRelativeDates(unknown, base)
	require.Nil(t, findEvent(unknown, "open_loops", "loop-1")["date"])

	// 0 events / nil state are safe.
	pipeline.AnchorRelativeDates(nil, base)
	pipeline.AnchorRelativeDates(map[string]any{}, base)

	// Past events transition, future events remain, and overrides drop superseded ids.
	state := map[string]any{
		"upcoming_events": []any{
			map[string]any{"id": "past-event", "title": "Past", "date": "2026-11-01"},
			map[string]any{"id": "future-event", "title": "Future", "date": "2026-12-01"},
			map[string]any{"id": "stale", "title": "Stale", "date": "2026-12-02"},
		},
	}
	pipeline.ApplyChronologicalOverrides(state, []string{"stale"})
	pipeline.TransitionPastEvents(state, base)

	require.Nil(t, findEvent(state, "upcoming_events", "stale"))
	require.Nil(t, findEvent(state, "upcoming_events", "past-event"))
	require.NotNil(t, findEvent(state, "upcoming_events", "future-event"))

	moved := requireEvent(t, state, "past_events", "past-event")
	require.Equal(t, "occurred", moved["status"])
}
