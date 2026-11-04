package pipeline_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"

	"github.com/spelech/gmessage-rag/internal/db"
	"github.com/spelech/gmessage-rag/internal/gm"
	"github.com/spelech/gmessage-rag/internal/pipeline"
)

// stubContactsFetcher is a contract stub for the external Google Contacts RPC boundary. The real
// gm.SyncContacts normalizer consumes its response.
type stubContactsFetcher struct {
	resp *gmproto.ListContactsResponse
}

func (s stubContactsFetcher) ListContacts(ctx context.Context) (*gmproto.ListContactsResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return s.resp, nil
}

// TestContactsSync_DossierEnrichment proves the Google Contacts roster enriches a persisted Living
// Dossier with ground-truth names, avatars, and labels. It runs against real Postgres; the only
// stubs are the external LLM and Google Contacts RPC boundaries.
//
// [Requirement(PIPE-04)]
func TestContactsSync_DossierEnrichment(t *testing.T) {
	database := openPipelineTestDB(t)
	ctx := context.Background()

	convID := fmt.Sprintf("conv-contacts-%d", time.Now().UnixNano())
	require.NoError(t, database.UpsertConversation(ctx, &db.Conversation{
		ID:           convID,
		Name:         "Jane Doe",
		Participants: []string{"+15551234567"},
		IsTracked:    true,
	}))

	base := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	summary := "Coffee plans for Friday."
	if _, err := database.UpsertSession(ctx, &db.ConversationSession{
		ConversationID: convID,
		StartedAt:      base,
		EndedAt:        base.Add(time.Hour),
		MessageCount:   1,
		Summary:        &summary,
		Transcript:     "[2026-11-02 09:00:00] Jane: Coffee on Friday?",
		IsProcessed:    true,
	}, nil); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	// Seed a prior dossier so enrichment is applied to an existing (versioned) record.
	priorProcessed := base.Add(-24 * time.Hour)
	_, err := database.UpsertContactDossier(ctx, convID, "Jane Doe",
		"# Living Dossier: Jane Doe\n\nOld notes.",
		map[string]any{"upcoming_events": []any{}},
		nil, &priorProcessed)
	require.NoError(t, err)

	// Contract stub for the external LLM boundary.
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"index": 0,
				"message": map[string]string{
					"role":    "assistant",
					"content": `{"markdown": "Jane is planning coffee.", "structured_state": {"upcoming_events": [], "open_loops": []}, "supersedes": []}`,
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

	// Contract stub for the external Google Contacts RPC boundary (real SyncContacts normalizes it).
	fetcher := stubContactsFetcher{resp: &gmproto.ListContactsResponse{
		Contacts: []*gmproto.Contact{{
			ContactID:      "c-1",
			ParticipantID:  "p-1",
			Name:           "Jane Doe",
			NameAgain:      "Janie",
			AvatarHexColor: "#4285F4",
			Number: &gmproto.ContactNumber{
				Number:        "+15551234567",
				MysteriousInt: 2,
			},
		}},
	}}

	outDir := t.TempDir()
	reconciler := pipeline.NewDossierReconciler(pipeline.DossierReconcilerConfig{
		Store:     database,
		Model:     model,
		OutputDir: outDir,
		Location:  time.UTC,
		Logger:    zerolog.Nop(),
		ContactSource: func(contactCtx context.Context) ([]gm.ContactSummary, error) {
			return gm.SyncContacts(contactCtx, fetcher)
		},
	})

	outcome, err := reconciler.Reconcile(ctx, convID)
	require.NoError(t, err)
	require.True(t, outcome.Changed)

	// Observable DB state: ground-truth contact metadata persisted in structured_state.
	dossier, err := database.GetContactDossier(ctx, convID)
	require.NoError(t, err)
	require.Equal(t, 2, dossier.Version)

	contact, ok := dossier.StructuredState["contact"].(map[string]any)
	require.True(t, ok, "expected structured_state.contact to be populated")
	require.Equal(t, "Jane Doe", contact["name"])
	require.Equal(t, "Janie", contact["nickname"])
	require.Equal(t, "#4285F4", contact["avatar_hex_color"])
	require.Equal(t, "+15551234567", contact["number"])
	require.Contains(t, toStringSlice(contact["labels"]), "contact")

	// Audit snapshot appended for the prior dossier.
	history, err := database.GetDossierHistory(ctx, convID, 10)
	require.NoError(t, err)
	require.Len(t, history, 1)

	// Written Markdown carries the enriched contact identity.
	mdPath := filepath.Join(outDir, "jane-doe.md")
	content, err := os.ReadFile(mdPath)
	require.NoError(t, err)
	require.Contains(t, string(content), "Jane Doe")
	require.Contains(t, string(content), "#4285F4")
}
