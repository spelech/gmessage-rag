package db_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spelech/gmessage-rag/internal/db"
)

// TestDossierChangesSince_BriefingQuery covers ListDossierChangesSince, which backs the
// briefing API (TRANS-03). It asserts observable state: the change record pairs the
// preceding audit snapshot with the current dossier, and untracked rows are excluded.
func TestDossierChangesSince_BriefingQuery(t *testing.T) {
	database := getTestDB(t)
	defer database.Close()
	ctx := context.Background()

	convID := "conv-brief-" + uuid.New().String()
	require.NoError(t, database.UpsertConversation(ctx, &db.Conversation{
		ID:        convID,
		Name:      "Briefing Test",
		IsTracked: true,
	}))

	windowStart := time.Now().UTC().Add(-time.Second)

	// Initial dossier (version 1), then an update (version 2) so a history snapshot exists.
	_, err := database.UpsertContactDossier(ctx, convID, "Briefing Test", "# v1", map[string]any{"n": 1}, nil, nil)
	require.NoError(t, err)
	_, err = database.UpsertContactDossier(ctx, convID, "Briefing Test", "# v2", map[string]any{"n": 2}, nil, nil)
	require.NoError(t, err)

	changes, err := database.ListDossierChangesSince(ctx, windowStart)
	require.NoError(t, err)

	var change *db.DossierChange
	for i := range changes {
		if changes[i].ConversationID == convID {
			change = &changes[i]
		}
	}
	require.NotNil(t, change, "expected a change record for the seeded conversation")
	assert.Equal(t, 2, change.CurrentVersion)
	require.NotNil(t, change.PreviousVersion)
	assert.Equal(t, 1, *change.PreviousVersion)
	assert.Equal(t, "# v2", change.CurrentMarkdown)
	assert.Equal(t, "# v1", change.PreviousMarkdown)
	require.NotNil(t, change.CurrentState)
	assert.Equal(t, float64(2), change.CurrentState["n"])
	require.NotNil(t, change.PreviousState)
	assert.Equal(t, float64(1), change.PreviousState["n"])

	// A window that starts after every update returns nothing for these rows.
	future, err := database.ListDossierChangesSince(ctx, time.Now().UTC().Add(time.Hour))
	require.NoError(t, err)
	for _, c := range future {
		assert.NotEqual(t, convID, c.ConversationID, "no change should be reported for a future window")
	}

	// A tracked conversation with only an initial version reports no previous snapshot.
	convInitial := "conv-brief-init-" + uuid.New().String()
	require.NoError(t, database.UpsertConversation(ctx, &db.Conversation{
		ID: convInitial, Name: "Initial Only", IsTracked: true,
	}))
	_, err = database.UpsertContactDossier(ctx, convInitial, "Initial Only", "# only", map[string]any{"n": 0}, nil, nil)
	require.NoError(t, err)

	// Untracked conversations are excluded.
	convUntracked := "conv-brief-untracked-" + uuid.New().String()
	require.NoError(t, database.UpsertConversation(ctx, &db.Conversation{
		ID: convUntracked, Name: "Untracked", IsTracked: false,
	}))
	_, err = database.UpsertContactDossier(ctx, convUntracked, "Untracked", "# hidden", map[string]any{"n": 9}, nil, nil)
	require.NoError(t, err)

	all, err := database.ListDossierChangesSince(ctx, time.Now().UTC().Add(-time.Second))
	require.NoError(t, err)

	seen := map[string]db.DossierChange{}
	for _, c := range all {
		seen[c.ConversationID] = c
	}

	initial, ok := seen[convInitial]
	require.True(t, ok, "tracked conversation with an initial version must appear")
	assert.Nil(t, initial.PreviousVersion)
	assert.Equal(t, "# only", initial.CurrentMarkdown)

	_, hasUntracked := seen[convUntracked]
	assert.False(t, hasUntracked, "untracked conversations must be excluded")

	// Nil pool fails closed.
	var nilDB *db.DB
	_, err = nilDB.ListDossierChangesSince(ctx, windowStart)
	assert.Error(t, err)
}
