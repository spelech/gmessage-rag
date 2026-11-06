package pipeline_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/spelech/gmessage-rag/internal/db"
	"github.com/spelech/gmessage-rag/internal/pipeline"
	"github.com/stretchr/testify/require"
)

func TestDossierReconciliation_AntecedentHopping(t *testing.T) {
	database := openPipelineTestDB(t)
	ctx := context.Background()

	convID := fmt.Sprintf("conv-dossier-hop-%d", time.Now().UnixNano())
	require.NoError(t, database.UpsertConversation(ctx, &db.Conversation{
		ID:           convID,
		Name:         "Neil Pelech",
		Participants: []string{"+15551234567"},
		IsTracked:    true,
	}))

	now := time.Date(2026, 11, 2, 14, 0, 0, 0, time.UTC)

	// Historical Session 1 (2 weeks ago): Neil mentions needing to donate his computer chair
	s1Summary := "Donating computer chair: Neil asks if Steven wants to keep or donate the chair."
	s1, err := database.UpsertSession(ctx, &db.ConversationSession{
		ConversationID: convID,
		StartedAt:      now.Add(-14 * 24 * time.Hour),
		EndedAt:        now.Add(-14*24*time.Hour + time.Hour),
		MessageCount:   2,
		Summary:        &s1Summary,
		Transcript:     "[2026-10-19 14:00:00] Neil: What should we do with that old computer chair?",
		IsProcessed:    true,
	}, nil)
	require.NoError(t, err)

	// New Session 2 (today): Steven replies about the chair
	s2Summary := "Donating chair followup: confirmed to donate it to Goodwill tomorrow."
	s2, err := database.UpsertSession(ctx, &db.ConversationSession{
		ConversationID: convID,
		StartedAt:      now,
		EndedAt:        now.Add(30 * time.Minute),
		MessageCount:   2,
		Summary:        &s2Summary,
		Transcript:     "[2026-11-02 14:00:00] Steven: Go ahead and donate that chair to Goodwill tomorrow.",
		IsProcessed:    true,
	}, nil)
	require.NoError(t, err)

	// Insert causal message_link connecting Session 2 -> Session 1
	_, err = database.InsertMessageLink(ctx, &db.MessageLink{
		ConversationID:  convID,
		SourceSessionID: s2.ID,
		TargetSessionID: s1.ID,
		LinkType:        "open_loop_resolution",
		Confidence:      0.95,
		Evidence: map[string]any{
			"open_loop_id": "computer_chair",
			"reason":       "Chair donation decision resolves Oct 19 question",
		},
	})
	require.NoError(t, err)

	// Seed prior dossier from before Session 2
	priorProcessed := now.Add(-24 * time.Hour)
	_, err = database.UpsertContactDossier(ctx, convID, "Neil Pelech",
		"# Living Dossier: Neil Pelech\n\nNotes.",
		map[string]any{
			"open_loops": []any{
				map[string]any{
					"id":         "chair-loop",
					"title":      "Old computer chair disposition",
					"status":     "open",
					"session_id": float64(s1.ID),
				},
			},
		},
		nil, &priorProcessed)
	require.NoError(t, err)

	var capturedUserPrompt string
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var chatReq struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.Unmarshal(body, &chatReq)
		for _, m := range chatReq.Messages {
			if m.Role == "user" {
				capturedUserPrompt = m.Content
			}
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]any{
					"role": "assistant",
					"content": `{
						"markdown": "Neil is donating his old computer chair to Goodwill tomorrow.",
						"structured_state": {
							"upcoming_events": [
								{"id": "evt-chair", "title": "Donate computer chair to Goodwill", "relative_date": "tomorrow"}
							],
							"open_loops": [
								{"id": "chair-loop", "title": "Old computer chair disposition", "status": "resolved"}
							]
						},
						"supersedes": []
					}`,
				},
			}},
		})
	}))
	defer stub.Close()

	model := pipeline.NewOpenAIDossierModel(pipeline.DossierModelConfig{
		BaseURL: stub.URL,
		APIKey:  "test-key",
		Model:   "test-model",
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

	// Assert the prompt contained the antecedent historical session context
	require.Contains(t, capturedUserPrompt, "ANTECEDENT HISTORICAL SESSIONS (LINKED BY OPEN LOOPS / REPLIES):")
	require.Contains(t, capturedUserPrompt, "What should we do with that old computer chair?")

	// Assert relative date was properly anchored to Session 2's date (2026-11-02 -> 2026-11-03)
	dossier, err := database.GetContactDossier(ctx, convID)
	require.NoError(t, err)
	upcoming := requireEvent(t, dossier.StructuredState, "upcoming_events", "evt-chair")
	require.Equal(t, "2026-11-03", upcoming["date"])
}
