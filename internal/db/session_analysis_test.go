package db_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/pgvector/pgvector-go"
	"github.com/stretchr/testify/require"

	"github.com/spelech/gmessage-rag/internal/db"
)

// TestSessions_NeedingAnalysis_MessagesAndUpdate covers the query, message
// lookup, and analysis update used by re-summarization.
//
// [Requirement(DB-09)]
func TestSessions_NeedingAnalysis_MessagesAndUpdate(t *testing.T) {
	database := getTestDB(t)
	defer database.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	convID := fmt.Sprintf("conv-sa-%d", time.Now().UnixNano())
	_, err := database.Pool.Exec(ctx,
		`INSERT INTO conversations (id, name, participants, is_group, is_tracked) VALUES ($1, $2, '[]'::jsonb, false, true)`,
		convID, "Session Analysis Contact")
	require.NoError(t, err)

	base := time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)
	var sessID int64
	require.NoError(t, database.Pool.QueryRow(ctx, `
		INSERT INTO conversation_sessions (conversation_id, started_at, ended_at, message_count, transcript, is_processed)
		VALUES ($1, $2, $3, 1, 'transcript', false) RETURNING id`,
		convID, base, base).Scan(&sessID))

	msgID := convID + "-m"
	_, err = database.Pool.Exec(ctx, `
		INSERT INTO messages (id, conversation_id, session_id, sender_id, sender_name, is_from_me, sent_at, body)
		VALUES ($1, $2, $3, 'peer', 'Peer', false, $4, 'hello')`,
		msgID, convID, sessID, base)
	require.NoError(t, err)

	needing, err := database.ListSessionsNeedingAnalysis(ctx, convID, 100)
	require.NoError(t, err)
	require.Len(t, needing, 1)
	require.Equal(t, sessID, needing[0].ID)

	msgs, err := database.GetSessionMessages(ctx, sessID)
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	require.Equal(t, msgID, msgs[0].ID)

	vec := make([]float32, 1536)
	vec[0] = 1
	hv := pgvector.NewHalfVector(vec)
	require.NoError(t, database.UpdateSessionAnalysis(ctx, sessID, "Title: summary", &hv))

	after, err := database.ListSessionsNeedingAnalysis(ctx, convID, 100)
	require.NoError(t, err)
	require.Len(t, after, 0, "updated session must no longer need analysis")

	// Not-found and invalid-id branches.
	require.ErrorIs(t, database.UpdateSessionAnalysis(ctx, 9223372036854775807, "x", nil), db.ErrNotFound)
	require.Error(t, database.UpdateSessionAnalysis(ctx, 0, "x", nil))
}
