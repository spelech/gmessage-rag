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

	// 5. Insert unprocessed attachment and test GetUnprocessedMediaAttachments & UpdateMediaAttachmentVision
	unprocessedMedia := &db.MediaAttachment{
		MessageID:       msgID,
		ConversationID:  convID,
		SHA256Hash:      "abcd000000000000000000000000000000000000000000000000000000001234",
		RelativePath:    "ab/cd/abcd00000000000000000000000000000000000000000000000000001234.jpg",
		MimeType:        "image/jpeg",
		FileSizeBytes:   1024,
		VisionProcessed: false,
	}
	err = database.UpsertMediaAttachment(ctx, unprocessedMedia)
	require.NoError(t, err)
	assert.True(t, unprocessedMedia.ID > 0)

	// Query unprocessed
	unprocessedList, err := database.GetUnprocessedMediaAttachments(ctx, convID, 10)
	require.NoError(t, err)
	require.NotEmpty(t, unprocessedList)
	var foundID int64
	for _, item := range unprocessedList {
		if item.ID == unprocessedMedia.ID {
			foundID = item.ID
			break
		}
	}
	assert.Equal(t, unprocessedMedia.ID, foundID)

	// Update vision fields
	newOCR := "EXTRACTED RECEIPT TOTAL: $42.50"
	newDesc := "A receipt from a grocery store"
	err = database.UpdateMediaAttachmentVision(ctx, unprocessedMedia.ID, &newOCR, &newDesc, true)
	require.NoError(t, err)

	// Update non-existent record returns ErrNotFound
	err = database.UpdateMediaAttachmentVision(ctx, 999999999, &newOCR, &newDesc, true)
	assert.ErrorIs(t, err, db.ErrNotFound)

	// Verify no longer in unprocessed list
	unprocessedAfter, err := database.GetUnprocessedMediaAttachments(ctx, convID, 10)
	require.NoError(t, err)
	for _, item := range unprocessedAfter {
		assert.NotEqual(t, unprocessedMedia.ID, item.ID)
	}

	// Verify updated fields in message attachments
	updatedAtts, err := database.GetMediaAttachmentsForMessage(ctx, msgID)
	require.NoError(t, err)
	var updatedItem *db.MediaAttachment
	for _, it := range updatedAtts {
		if it.ID == unprocessedMedia.ID {
			itemCopy := it
			updatedItem = &itemCopy
			break
		}
	}
	require.NotNil(t, updatedItem)
	assert.True(t, updatedItem.VisionProcessed)
	require.NotNil(t, updatedItem.OCRText)
	assert.Equal(t, newOCR, *updatedItem.OCRText)
	require.NotNil(t, updatedItem.VisualDescription)
	assert.Equal(t, newDesc, *updatedItem.VisualDescription)
}

func TestMediaAttachments_NilDBValidation(t *testing.T) {
	var database *db.DB
	ctx := context.Background()

	err := database.UpdateMediaAttachmentVision(ctx, 1, nil, nil, true)
	assert.Error(t, err)

	list, err := database.GetUnprocessedMediaAttachments(ctx, "", 10)
	assert.Error(t, err)
	assert.Nil(t, list)
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
	// CountUnindexedMessages is global; other package test binaries write to the
	// shared database concurrently, so assert a monotonic increase rather than an
	// exact delta. Scoped correctness is proven against this conversation below.
	assert.GreaterOrEqual(t, newUnindexed, initialUnindexed+2)

	unassigned, err := database.GetUnassignedMessages(ctx, convID)
	require.NoError(t, err)
	assert.Len(t, unassigned, 2)

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

// TestMediaAttachments_Search covers SearchMediaAttachments, consumed by the
// search_shared_media MCP tool (MCP-05).
func TestMediaAttachments_Search(t *testing.T) {
	database := getTestDB(t)
	defer database.Close()
	ctx := context.Background()

	convID := "conv-media-search-" + uuid.New().String()
	require.NoError(t, database.UpsertConversation(ctx, &db.Conversation{ID: convID, Name: "Media Search"}))

	msgID := "msg-media-search-" + uuid.New().String()
	now := time.Now().UTC().Truncate(time.Microsecond)
	require.NoError(t, database.UpsertMessage(ctx, &db.Message{
		ID:             msgID,
		ConversationID: convID,
		SenderID:       "sender-1",
		SenderName:     "Alice",
		SentAt:         now,
		Body:           "attachment",
	}))

	// Unique search tokens keep this test independent of other rows in the shared DB.
	ocrToken := "invoice-" + uuid.New().String()
	ocr := "INV " + ocrToken
	desc := "A scanned grocery receipt"
	require.NoError(t, database.UpsertMediaAttachment(ctx, &db.MediaAttachment{
		MessageID:         msgID,
		ConversationID:    convID,
		SHA256Hash:        uuid.New().String() + uuid.New().String(),
		RelativePath:      "aa/a1/search-1.png",
		MimeType:          "image/png",
		FileSizeBytes:     10,
		OCRText:           &ocr,
		VisualDescription: &desc,
		VisionProcessed:   true,
	}))

	descToken := "sunset-" + uuid.New().String()
	desc2 := descToken + " over mountain ridges"
	require.NoError(t, database.UpsertMediaAttachment(ctx, &db.MediaAttachment{
		MessageID:         msgID,
		ConversationID:    convID,
		SHA256Hash:        uuid.New().String() + uuid.New().String(),
		RelativePath:      "aa/a1/search-2.jpg",
		MimeType:          "image/jpeg",
		FileSizeBytes:     12,
		VisualDescription: &desc2,
		VisionProcessed:   true,
	}))

	// OCR-text match, scoped to the conversation.
	hits, err := database.SearchMediaAttachments(ctx, convID, ocrToken, 10)
	require.NoError(t, err)
	require.Len(t, hits, 1)
	require.NotNil(t, hits[0].OCRText)
	assert.Equal(t, ocr, *hits[0].OCRText)

	// Visual-description match across all conversations (empty scope).
	hits, err = database.SearchMediaAttachments(ctx, "", descToken, 10)
	require.NoError(t, err)
	require.Len(t, hits, 1)
	require.NotNil(t, hits[0].VisualDescription)
	assert.Equal(t, desc2, *hits[0].VisualDescription)

	// Empty query matches every attachment in scope.
	all, err := database.SearchMediaAttachments(ctx, convID, "", 10)
	require.NoError(t, err)
	assert.Len(t, all, 2)

	var nilDB *db.DB
	_, err = nilDB.SearchMediaAttachments(ctx, convID, "x", 10)
	assert.Error(t, err)
}
