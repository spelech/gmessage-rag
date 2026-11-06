package db_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/spelech/gmessage-rag/internal/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMessageLinks_CRUDAndAntecedentQuery(t *testing.T) {
	database := getTestDB(t)
	defer database.Close()

	ctx := context.Background()
	convID := fmt.Sprintf("conv-links-%d", time.Now().UnixNano())

	// Insert test conversation
	_, err := database.Pool.Exec(ctx, `
		INSERT INTO conversations (id, name, participants, is_group, is_tracked)
		VALUES ($1, $2, '["+15551234567"]'::jsonb, false, true)
	`, convID, "Links Test Contact")
	require.NoError(t, err)

	now := time.Now().UTC().Truncate(time.Microsecond)
	s1 := &db.ConversationSession{
		ConversationID: convID,
		StartedAt:      now.Add(-48 * time.Hour),
		EndedAt:        now.Add(-47 * time.Hour),
		MessageCount:   2,
		Summary:        ptrString("Medical discussion: Dr. Smith consultation planned"),
		Transcript:     "[10:00] Initial consultation with Dr. Smith",
		IsProcessed:    true,
	}
	s1, err = database.UpsertSession(ctx, s1, nil)
	require.NoError(t, err)

	s2 := &db.ConversationSession{
		ConversationID: convID,
		StartedAt:      now,
		EndedAt:        now.Add(15 * time.Minute),
		MessageCount:   2,
		Summary:        ptrString("Appointment update: consultation is set for next Tuesday"),
		Transcript:     "[14:00] Heard back from doctor, appointment next Tuesday",
		IsProcessed:    true,
	}
	s2, err = database.UpsertSession(ctx, s2, nil)
	require.NoError(t, err)

	// Insert link connecting s2 (source) to s1 (target antecedent)
	link := &db.MessageLink{
		ConversationID:  convID,
		SourceSessionID: s2.ID,
		TargetSessionID: s1.ID,
		LinkType:        "open_loop_resolution",
		Confidence:      0.95,
		Evidence: map[string]any{
			"open_loop_id": "dr_smith",
			"reason":       "Appointment confirmation references initial consultation",
		},
	}

	saved, err := database.InsertMessageLink(ctx, link)
	require.NoError(t, err)
	require.NotZero(t, saved.ID)
	assert.Equal(t, "detected", saved.Status)

	// Query links for session s2
	links, err := database.GetLinksForSession(ctx, s2.ID)
	require.NoError(t, err)
	require.Len(t, links, 1)
	assert.Equal(t, s1.ID, links[0].TargetSessionID)
	assert.Equal(t, "open_loop_resolution", links[0].LinkType)

	// Query antecedent sessions
	antecedents, err := database.GetAntecedentSessions(ctx, []int64{s2.ID})
	require.NoError(t, err)
	require.Len(t, antecedents, 1)
	assert.Equal(t, s1.ID, antecedents[0].ID)
	assert.Contains(t, *antecedents[0].Summary, "Dr. Smith consultation")
}

func ptrString(s string) *string {
	return &s
}
