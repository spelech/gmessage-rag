package db_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spelech/gmessage-rag/internal/db"
)

func TestMediaAttachments_CRUDAndQuery(t *testing.T) {
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

	convID := "conv-media-" + uuid.New().String()
	require.NoError(t, database.UpsertConversation(ctx, &db.Conversation{
		ID:   convID,
		Name: "Media Test Conversation",
	}))

	msgID := "msg-media-" + uuid.New().String()
	now := time.Now().UTC().Truncate(time.Microsecond)
	statusVal := int32(1)
	msg := &db.Message{
		ID:             msgID,
		ConversationID: convID,
		SenderID:       "sender-1",
		SenderName:     "Alice",
		IsFromMe:       false,
		SentAt:         now,
		Body:           "Here is an attached image of the blueprint",
		RawStatus:      &statusVal,
	}
	require.NoError(t, database.UpsertMessage(ctx, msg))

	// 1. Nil validation on UpsertMediaAttachment
	assert.Error(t, database.UpsertMediaAttachment(ctx, nil))

	// 2. Insert valid media attachment
	width := 1920
	height := 1080
	ocrText := "BLUEPRINT REV A"
	visualDesc := "Architectural schematic diagram"
	media := &db.MediaAttachment{
		MessageID:         msgID,
		ConversationID:    convID,
		SHA256Hash:        "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		RelativePath:      "e3/b0/e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855.png",
		MimeType:          "image/png",
		FileSizeBytes:     204800,
		Width:             &width,
		Height:            &height,
		OCRText:           &ocrText,
		VisualDescription: &visualDesc,
		VisionProcessed:   true,
	}

	err = database.UpsertMediaAttachment(ctx, media)
	require.NoError(t, err)
	assert.True(t, media.ID > 0)
	assert.False(t, media.CreatedAt.IsZero())

	// 3. Upsert duplicate media attachment (ON CONFLICT DO NOTHING) is idempotent
	err = database.UpsertMediaAttachment(ctx, media)
	require.NoError(t, err)

	// 4. Retrieve media attachments for message
	attachments, err := database.GetMediaAttachmentsForMessage(ctx, msgID)
	require.NoError(t, err)
	require.Len(t, attachments, 1)
	assert.Equal(t, media.SHA256Hash, attachments[0].SHA256Hash)
	assert.Equal(t, media.RelativePath, attachments[0].RelativePath)
	assert.Equal(t, media.MimeType, attachments[0].MimeType)
	assert.Equal(t, int64(204800), attachments[0].FileSizeBytes)
	require.NotNil(t, attachments[0].Width)
	assert.Equal(t, 1920, *attachments[0].Width)
	require.NotNil(t, attachments[0].OCRText)
	assert.Equal(t, "BLUEPRINT REV A", *attachments[0].OCRText)

	// Retrieve for message with no attachments returns empty slice
	emptyList, err := database.GetMediaAttachmentsForMessage(ctx, "nonexistent-msg-id")
	require.NoError(t, err)
	assert.Empty(t, emptyList)
}

func TestMessages_CountUnindexedAndFTS(t *testing.T) {
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

	convID := "conv-fts-" + uuid.New().String()
	require.NoError(t, database.UpsertConversation(ctx, &db.Conversation{
		ID:   convID,
		Name: "FTS Test Conversation",
	}))

	// 1. Count unindexed messages before inserting
	initialUnindexed, err := database.CountUnindexedMessages(ctx)
	require.NoError(t, err)

	// Insert 2 messages without session_id
	statusVal := int32(1)
	msg1 := &db.Message{
		ID:             "msg-fts-1-" + uuid.New().String(),
		ConversationID: convID,
		SenderID:       "alice",
		SenderName:     "Alice",
		SentAt:         time.Now().UTC().Add(-2 * time.Minute),
		Body:           "Deploying quantum reactor simulation harness",
		RawStatus:      &statusVal,
	}
	msg2 := &db.Message{
		ID:             "msg-fts-2-" + uuid.New().String(),
		ConversationID: convID,
		SenderID:       "bob",
		SenderName:     "Bob",
		SentAt:         time.Now().UTC().Add(-1 * time.Minute),
		Body:           "Telemetry received for quantum reactor diagnostics",
		RawStatus:      &statusVal,
	}
	_, err = database.UpsertMessagesBatch(ctx, []*db.Message{msg1, msg2})
	require.NoError(t, err)

	// Verify count increased by 2
	newUnindexed, err := database.CountUnindexedMessages(ctx)
	require.NoError(t, err)
	assert.Equal(t, initialUnindexed+2, newUnindexed)

	// 2. Full-Text Search (SearchMessagesFTS)
	results, err := database.SearchMessagesFTS(ctx, convID, "quantum reactor", 10)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(results), 2)
	assert.Contains(t, results[0].Body, "quantum reactor")

	// Search across all conversations by passing empty string for conversationID
	globalResults, err := database.SearchMessagesFTS(ctx, "", "quantum reactor", 10)
	require.NoError(t, err)
	assert.NotEmpty(t, globalResults)

	// Search with no matching query
	noMatch, err := database.SearchMessagesFTS(ctx, convID, "supercalifragilisticexpialidocious", 10)
	require.NoError(t, err)
	assert.Empty(t, noMatch)

	// 3. SearchSessionsHybrid_RRF convenience call
	summaryText := "Discussion of quantum reactor telemetry"
	session := &db.ConversationSession{
		ConversationID: convID,
		StartedAt:      time.Now().UTC().Add(-10 * time.Minute),
		EndedAt:        time.Now().UTC(),
		MessageCount:   2,
		Summary:        &summaryText,
		Transcript:     "Alice: Deploying quantum reactor. Bob: Telemetry received.",
		IsProcessed:    true,
	}
	_, err = database.UpsertSession(ctx, session, []string{msg1.ID, msg2.ID})
	require.NoError(t, err)

	sessionRes, err := database.SearchSessionsHybrid_RRF(ctx, convID, "quantum", 5)
	require.NoError(t, err)
	assert.NotEmpty(t, sessionRes)

	// 4. Edge cases & input validation boundaries
	assert.Error(t, database.UpsertMessage(ctx, nil))
	_, err = database.GetContactDossier(ctx, "")
	assert.Error(t, err)
	_, err = database.UpsertContactDossier(ctx, "", "Alice", "# Alice", nil, nil, nil)
	assert.Error(t, err)
}
