package db_test

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spelech/gmessage-rag/internal/db"
)

func TestConversations_TrackingAndBackfillCursor(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://jules:@127.0.0.1:5432/gmessages?sslmode=disable"
	}

	ctx := context.Background()
	database, err := db.Connect(ctx, dbURL)
	if err != nil {
		t.Skipf("skipping test: database connection failed: %v", err)
		return
	}
	defer database.Close()

	require.NoError(t, database.Migrate(ctx))

	convID := "conv-track-" + uuid.New().String()
	conv := &db.Conversation{
		ID:        convID,
		Name:      "Track Test Conversation",
		IsGroup:   false,
		IsTracked: false,
	}

	require.NoError(t, database.UpsertConversation(ctx, conv))

	// 1. Verify initially not tracked
	fetched, err := database.GetConversation(ctx, convID)
	require.NoError(t, err)
	assert.False(t, fetched.IsTracked)

	// 2. Set tracked = true
	err = database.SetConversationTracked(ctx, convID, true)
	require.NoError(t, err)

	fetched, err = database.GetConversation(ctx, convID)
	require.NoError(t, err)
	assert.True(t, fetched.IsTracked)

	// Set tracked for nonexistent conversation returns ErrNotFound
	err = database.SetConversationTracked(ctx, "nonexistent-conv-id", true)
	assert.ErrorIs(t, err, db.ErrNotFound)

	// 3. Update backfill cursor
	cursorVal := "cursor_token_12345"
	err = database.UpdateConversationBackfillCursor(ctx, convID, &cursorVal)
	require.NoError(t, err)

	fetched, err = database.GetConversation(ctx, convID)
	require.NoError(t, err)
	require.NotNil(t, fetched.BackfillCursor)
	assert.Equal(t, cursorVal, *fetched.BackfillCursor)

	// Update backfill cursor with nil clears it
	err = database.UpdateConversationBackfillCursor(ctx, convID, nil)
	require.NoError(t, err)

	fetched, err = database.GetConversation(ctx, convID)
	require.NoError(t, err)
	assert.Nil(t, fetched.BackfillCursor)

	// Validation checks
	assert.Error(t, database.UpdateConversationBackfillCursor(ctx, "", &cursorVal))
	assert.ErrorIs(t, database.UpdateConversationBackfillCursor(ctx, "nonexistent-conv-id", &cursorVal), db.ErrNotFound)
}

func TestConversations_ListAndGetNotFound(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://jules:@127.0.0.1:5432/gmessages?sslmode=disable"
	}

	ctx := context.Background()
	database, err := db.Connect(ctx, dbURL)
	if err != nil {
		t.Skipf("skipping test: database connection failed: %v", err)
		return
	}
	defer database.Close()

	// 1. GetConversation on nonexistent ID returns ErrNotFound
	_, err = database.GetConversation(ctx, "nonexistent-conversation-xyz")
	assert.ErrorIs(t, err, db.ErrNotFound)

	// 2. ListConversations with default limit (limit <= 0)
	convs, err := database.ListConversations(ctx, false, 0)
	require.NoError(t, err)
	assert.NotNil(t, convs)

	// 3. ListConversations with trackedOnly = true
	trackedConvs, err := database.ListConversations(ctx, true, 10)
	require.NoError(t, err)
	assert.NotNil(t, trackedConvs)
	for _, c := range trackedConvs {
		assert.True(t, c.IsTracked)
	}
}

// TestConversations_ResetBackfill covers ResetConversationBackfill, consumed by the
// manage_tracked_contact MCP tool (MCP-06).
func TestConversations_ResetBackfill(t *testing.T) {
	database := getTestDB(t)
	defer database.Close()
	ctx := context.Background()

	convID := "conv-reset-" + uuid.New().String()
	require.NoError(t, database.UpsertConversation(ctx, &db.Conversation{
		ID:   convID,
		Name: "Reset Backfill Conversation",
	}))

	// Seed a cursor and a completed backfill, then reset both.
	cursorVal := "cursor_token_reset"
	require.NoError(t, database.UpdateConversationBackfillCursor(ctx, convID, &cursorVal))
	_, err := database.Pool.Exec(ctx, `UPDATE conversations SET backfill_complete = TRUE WHERE id = $1`, convID)
	require.NoError(t, err)

	require.NoError(t, database.ResetConversationBackfill(ctx, convID))

	fetched, err := database.GetConversation(ctx, convID)
	require.NoError(t, err)
	assert.Nil(t, fetched.BackfillCursor)
	assert.False(t, fetched.BackfillComplete)

	// Validation and not-found paths.
	assert.Error(t, database.ResetConversationBackfill(ctx, ""))
	assert.ErrorIs(t, database.ResetConversationBackfill(ctx, "nonexistent-conv-id"), db.ErrNotFound)

	var nilDB *db.DB
	assert.Error(t, nilDB.ResetConversationBackfill(ctx, convID))
}
