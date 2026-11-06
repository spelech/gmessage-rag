package pipeline_test

import (
	"context"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/spelech/gmessage-rag/internal/db"
	"github.com/spelech/gmessage-rag/internal/pipeline"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockOpenLoopStore struct {
	links   []db.MessageLink
	dossier *db.ContactDossier
}

func (m *mockOpenLoopStore) GetLinksForSession(ctx context.Context, sessionID int64) ([]db.MessageLink, error) {
	var out []db.MessageLink
	for _, l := range m.links {
		if l.SourceSessionID == sessionID {
			out = append(out, l)
		}
	}
	return out, nil
}

func (m *mockOpenLoopStore) InsertMessageLink(ctx context.Context, link *db.MessageLink) (*db.MessageLink, error) {
	link.ID = int64(len(m.links) + 1)
	link.CreatedAt = time.Now()
	m.links = append(m.links, *link)
	return link, nil
}

func (m *mockOpenLoopStore) GetContactDossier(ctx context.Context, contactIdentifier string) (*db.ContactDossier, error) {
	return m.dossier, nil
}

func TestExtractActiveOpenLoops(t *testing.T) {
	dossier := &db.ContactDossier{
		ConversationID: "conv-1",
		StructuredState: map[string]any{
			"open_loops": []any{
				map[string]any{
					"id":         "loop-1",
					"title":      "Follow up with Dr. Smith",
					"status":     "open",
					"session_id": float64(10),
				},
				map[string]any{
					"id":         "loop-2",
					"title":      "Donate old chair",
					"status":     "resolved",
					"session_id": float64(12),
				},
				map[string]any{
					"id":         "loop-3",
					"title":      "Sim rig aluminum profile order",
					"status":     "pending",
					"session_id": float64(15),
				},
			},
		},
	}

	active := pipeline.ExtractActiveOpenLoops(dossier)
	require.Len(t, active, 2)
	assert.Equal(t, "loop-1", active[0].ID)
	assert.Equal(t, int64(10), active[0].SessionID)
	assert.Equal(t, "loop-3", active[1].ID)
	assert.Equal(t, int64(15), active[1].SessionID)
}

func TestOpenLoopLinker_LexicalMatchAndRecord(t *testing.T) {
	dossier := &db.ContactDossier{
		ConversationID: "conv-1",
		StructuredState: map[string]any{
			"open_loops": []any{
				map[string]any{
					"id":         "loop-medical",
					"title":      "Dr. Smith",
					"status":     "pending",
					"session_id": float64(101),
				},
			},
		},
	}

	store := &mockOpenLoopStore{dossier: dossier}
	linker := pipeline.NewOpenLoopLinker(store, nil, zerolog.Nop())

	session := &db.ConversationSession{
		ID:             205,
		ConversationID: "conv-1",
		Transcript:     "[2026-11-06 14:00] Alice: Just heard back from Dr. Smith, the consultation is set!",
	}

	links, err := linker.DetectAndRecordLinks(context.Background(), "conv-1", session)
	require.NoError(t, err)
	require.Len(t, links, 1)

	link := links[0]
	assert.Equal(t, int64(205), link.SourceSessionID)
	assert.Equal(t, int64(101), link.TargetSessionID)
	assert.Equal(t, "antecedent_continuation", link.LinkType)
	assert.Equal(t, "detector:open_loops", link.CreatedBy)
	assert.Equal(t, "loop-medical", link.Evidence["open_loop_id"])
}
