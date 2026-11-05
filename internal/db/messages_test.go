package db_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/spelech/gmessage-rag/internal/db"
)

// [Requirement(DB-02)]
func TestMessages_UpsertConversation(t *testing.T) {
	database := getTestDB(t)
	defer database.Close()

	ctx := context.Background()
	convID := fmt.Sprintf("conv-crud-%d", time.Now().UnixNano())

	now := time.Now().UTC().Truncate(time.Microsecond)
	conv := &db.Conversation{
		ID:             convID,
		Name:           "Alice Smith",
		Participants:   []string{"+15550001111", "+15550002222"},
		IsGroup:        false,
		IsTracked:      true,
		LastMessageAt:  &now,
		BackfillCursor: nil,
		Metadata:       map[string]any{"source": "test"},
	}

	// 1. Initial Upsert
	if err := database.UpsertConversation(ctx, conv); err != nil {
		t.Fatalf("UpsertConversation failed: %v", err)
	}
	if conv.CreatedAt.IsZero() || conv.UpdatedAt.IsZero() {
		t.Fatalf("expected non-zero timestamps on UpsertConversation, got created_at=%v updated_at=%v", conv.CreatedAt, conv.UpdatedAt)
	}

	// 2. GetConversation
	fetched, err := database.GetConversation(ctx, convID)
	if err != nil {
		t.Fatalf("GetConversation failed: %v", err)
	}
	if fetched.ID != convID || fetched.Name != "Alice Smith" || !fetched.IsTracked || len(fetched.Participants) != 2 {
		t.Fatalf("unexpected fetched conversation: %+v", fetched)
	}

	// 3. Update Conversation fields with ON CONFLICT (id) DO UPDATE
	updatedTime := now.Add(10 * time.Minute)
	conv.Name = "Alice S."
	conv.IsTracked = false
	conv.LastMessageAt = &updatedTime
	if err := database.UpsertConversation(ctx, conv); err != nil {
		t.Fatalf("subsequent UpsertConversation failed: %v", err)
	}

	fetchedUpdated, err := database.GetConversation(ctx, convID)
	if err != nil {
		t.Fatalf("GetConversation after update failed: %v", err)
	}
	if fetchedUpdated.Name != "Alice S." || fetchedUpdated.IsTracked {
		t.Fatalf("expected updated name 'Alice S.' and is_tracked false, got %+v", fetchedUpdated)
	}

	// 4. GetConversation with non-existent ID
	_, err = database.GetConversation(ctx, "non-existent-conv-id")
	if !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for non-existent conversation, got %v", err)
	}
}

// [Requirement(DB-02)]
func TestMessages_ListConversations(t *testing.T) {
	database := getTestDB(t)
	defer database.Close()

	ctx := context.Background()
	tag := time.Now().UnixNano()

	// Seed 3 conversations: 2 tracked, 1 untracked
	c1ID := fmt.Sprintf("conv-list-1-%d", tag)
	c2ID := fmt.Sprintf("conv-list-2-%d", tag)
	c3ID := fmt.Sprintf("conv-list-3-%d", tag)

	t1 := time.Now().UTC().Add(-10 * time.Minute)
	t2 := time.Now().UTC().Add(-5 * time.Minute)
	t3 := time.Now().UTC()

	_ = database.UpsertConversation(ctx, &db.Conversation{
		ID:            c1ID,
		Name:          "User 1 Tracked Older",
		IsTracked:     true,
		LastMessageAt: &t1,
	})
	_ = database.UpsertConversation(ctx, &db.Conversation{
		ID:            c2ID,
		Name:          "User 2 Untracked Middle",
		IsTracked:     false,
		LastMessageAt: &t2,
	})
	_ = database.UpsertConversation(ctx, &db.Conversation{
		ID:            c3ID,
		Name:          "User 3 Tracked Newest",
		IsTracked:     true,
		LastMessageAt: &t3,
	})

	// List all conversations with limit
	allConvs, err := database.ListConversations(ctx, false, 100)
	if err != nil {
		t.Fatalf("ListConversations(all) failed: %v", err)
	}
	if len(allConvs) < 3 {
		t.Fatalf("expected at least 3 conversations, got %d", len(allConvs))
	}

	// List tracked only
	trackedConvs, err := database.ListConversations(ctx, true, 100)
	if err != nil {
		t.Fatalf("ListConversations(trackedOnly) failed: %v", err)
	}
	for _, c := range trackedConvs {
		if !c.IsTracked {
			t.Fatalf("expected only tracked conversations, found untracked conversation %s", c.ID)
		}
	}

	// Check limit
	limitedConvs, err := database.ListConversations(ctx, false, 2)
	if err != nil {
		t.Fatalf("ListConversations(limit 2) failed: %v", err)
	}
	if len(limitedConvs) != 2 {
		t.Fatalf("expected exactly 2 conversations, got %d", len(limitedConvs))
	}
}

// [Requirement(DB-02)]
func TestUpsertMessages_Idempotent(t *testing.T) {
	database := getTestDB(t)
	defer database.Close()

	ctx := context.Background()
	convID := fmt.Sprintf("conv-idemp-%d", time.Now().UnixNano())

	if err := database.UpsertConversation(ctx, &db.Conversation{
		ID:   convID,
		Name: "Idempotency Test Thread",
	}); err != nil {
		t.Fatalf("create conversation failed: %v", err)
	}

	baseTime := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	msgID := fmt.Sprintf("msg-single-%d", time.Now().UnixNano())
	rawStatus := int32(1)

	msg := &db.Message{
		ID:             msgID,
		ConversationID: convID,
		SenderID:       "sender-me",
		SenderName:     "Me",
		IsFromMe:       true,
		SentAt:         baseTime,
		Body:           "Hello World idempotent test",
		RawStatus:      &rawStatus,
		Reactions:      []db.Reaction{{Sender: "Alice", Emoji: "👍"}},
	}

	// First insert
	if err := database.UpsertMessage(ctx, msg); err != nil {
		t.Fatalf("initial UpsertMessage failed: %v", err)
	}

	// Replay duplicate message - must not error and must not create duplicate
	if err := database.UpsertMessage(ctx, msg); err != nil {
		t.Fatalf("duplicate replay UpsertMessage failed: %v", err)
	}

	// Verify only 1 row exists
	var count int
	err := database.Pool.QueryRow(ctx, "SELECT COUNT(*) FROM messages WHERE id = $1", msgID).Scan(&count)
	if err != nil {
		t.Fatalf("query count failed: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 row after duplicate message replay, got %d", count)
	}
}

// [Requirement(DB-02)]
func TestMessages_Batch01NBoundaries(t *testing.T) {
	database := getTestDB(t)
	defer database.Close()

	ctx := context.Background()
	convID := fmt.Sprintf("conv-01n-%d", time.Now().UnixNano())

	if err := database.UpsertConversation(ctx, &db.Conversation{
		ID:   convID,
		Name: "Batch 0-1-N Test",
	}); err != nil {
		t.Fatalf("create conversation failed: %v", err)
	}

	baseTime := time.Date(2026, 11, 2, 14, 0, 0, 0, time.UTC)

	// Boundary 0: Empty batch
	n0, err := database.UpsertMessagesBatch(ctx, []*db.Message{})
	if err != nil {
		t.Fatalf("batch 0 failed: %v", err)
	}
	if n0 != 0 {
		t.Fatalf("expected 0 rows inserted for empty batch, got %d", n0)
	}

	// Boundary 1: Single message batch
	singleID := fmt.Sprintf("msg-batch-single-%d", time.Now().UnixNano())
	msg1 := &db.Message{
		ID:             singleID,
		ConversationID: convID,
		SenderID:       "sender-1",
		SenderName:     "Sender One",
		IsFromMe:       false,
		SentAt:         baseTime,
		Body:           "Single message in batch",
	}
	n1, err := database.UpsertMessagesBatch(ctx, []*db.Message{msg1})
	if err != nil {
		t.Fatalf("batch 1 failed: %v", err)
	}
	if n1 != 1 {
		t.Fatalf("expected 1 row inserted for single batch, got %d", n1)
	}

	// Boundary N: Large batch (N = 50 messages)
	const batchSize = 50
	nMsgs := make([]*db.Message, batchSize)
	for i := 0; i < batchSize; i++ {
		nMsgs[i] = &db.Message{
			ID:             fmt.Sprintf("msg-batch-n-%d-%d", time.Now().UnixNano(), i),
			ConversationID: convID,
			SenderID:       "sender-n",
			SenderName:     "Sender N",
			IsFromMe:       i%2 == 0,
			SentAt:         baseTime.Add(time.Duration(i) * time.Minute),
			Body:           fmt.Sprintf("Batch item message %d", i),
		}
	}

	nN, err := database.UpsertMessagesBatch(ctx, nMsgs)
	if err != nil {
		t.Fatalf("batch N failed: %v", err)
	}
	if nN != batchSize {
		t.Fatalf("expected %d rows inserted for batch N, got %d", batchSize, nN)
	}

	// Replay batch N (asserting idempotency on batch replay: 0 rows inserted)
	nReplay, err := database.UpsertMessagesBatch(ctx, nMsgs)
	if err != nil {
		t.Fatalf("replay batch N failed: %v", err)
	}
	if nReplay != 0 {
		t.Fatalf("expected 0 rows inserted on duplicate batch replay, got %d", nReplay)
	}

	// Verify conversation last_message_at was automatically updated to the latest message timestamp
	conv, err := database.GetConversation(ctx, convID)
	if err != nil {
		t.Fatalf("GetConversation failed: %v", err)
	}
	expectedLatest := baseTime.Add(time.Duration(batchSize-1) * time.Minute)
	if conv.LastMessageAt == nil || !conv.LastMessageAt.Equal(expectedLatest) {
		t.Fatalf("expected conversation last_message_at to be %v, got %v", expectedLatest, conv.LastMessageAt)
	}
}

// [Requirement(DB-02)]
func TestMessages_ReactionArrayAndDeduplication(t *testing.T) {
	database := getTestDB(t)
	defer database.Close()

	ctx := context.Background()
	convID := fmt.Sprintf("conv-react-%d", time.Now().UnixNano())

	if err := database.UpsertConversation(ctx, &db.Conversation{
		ID:   convID,
		Name: "Reactions Test Thread",
	}); err != nil {
		t.Fatalf("create conversation failed: %v", err)
	}

	msgID := fmt.Sprintf("msg-react-%d", time.Now().UnixNano())
	msg := &db.Message{
		ID:             msgID,
		ConversationID: convID,
		SenderID:       "sender-1",
		SenderName:     "Sender One",
		SentAt:         time.Now().UTC(),
		Body:           "Message for reactions test",
		Reactions:      []db.Reaction{},
	}
	if err := database.UpsertMessage(ctx, msg); err != nil {
		t.Fatalf("UpsertMessage failed: %v", err)
	}

	// Update reactions with duplicates
	reactionsWithDuplicates := []db.Reaction{
		{Sender: "Alice", Emoji: "❤️"},
		{Sender: "Bob", Emoji: "🔥"},
		{Sender: "Alice", Emoji: "❤️"}, // Duplicate reaction
		{Sender: "Alice", Emoji: "🎉"},  // Different emoji by same sender (allowed)
	}

	if err := database.UpdateReactions(ctx, msgID, reactionsWithDuplicates); err != nil {
		t.Fatalf("UpdateReactions failed: %v", err)
	}

	// Retrieve message and verify reactions
	msgs, err := database.GetMessagesAroundTime(ctx, convID, msg.SentAt, 5)
	if err != nil {
		t.Fatalf("GetMessagesAroundTime failed: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}

	reactions := msgs[0].Reactions
	if len(reactions) != 3 {
		t.Fatalf("expected 3 reactions after deduplication, got %d (%+v)", len(reactions), reactions)
	}

	if reactions[0].Sender != "Alice" || reactions[0].Emoji != "❤️" {
		t.Errorf("reaction 0 mismatch: %+v", reactions[0])
	}
	if reactions[1].Sender != "Bob" || reactions[1].Emoji != "🔥" {
		t.Errorf("reaction 1 mismatch: %+v", reactions[1])
	}
	if reactions[2].Sender != "Alice" || reactions[2].Emoji != "🎉" {
		t.Errorf("reaction 2 mismatch: %+v", reactions[2])
	}

	// Non-existent message returns ErrNotFound
	err = database.UpdateReactions(ctx, "non-existent-msg-id", reactions)
	if !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for non-existent message reactions update, got %v", err)
	}
}

// [Requirement(DB-02)]
func TestMessages_AddRemoveReaction(t *testing.T) {
	database := getTestDB(t)
	defer database.Close()

	ctx := context.Background()
	convID := fmt.Sprintf("conv-addreact-%d", time.Now().UnixNano())

	if err := database.UpsertConversation(ctx, &db.Conversation{
		ID:   convID,
		Name: "Add/Remove Reaction Test",
	}); err != nil {
		t.Fatalf("create conversation failed: %v", err)
	}

	msgID := fmt.Sprintf("msg-addreact-%d", time.Now().UnixNano())
	sentAt := time.Now().UTC().Truncate(time.Microsecond)
	if err := database.UpsertMessage(ctx, &db.Message{
		ID:             msgID,
		ConversationID: convID,
		SenderID:       "sender-1",
		SenderName:     "Sender One",
		SentAt:         sentAt,
		Body:           "add/remove reaction target",
		Reactions:      []db.Reaction{},
	}); err != nil {
		t.Fatalf("UpsertMessage failed: %v", err)
	}

	// Nominal add and idempotent re-add.
	for i := 0; i < 2; i++ {
		if err := database.AddReaction(ctx, msgID, "me", "👍"); err != nil {
			t.Fatalf("AddReaction attempt %d failed: %v", i+1, err)
		}
	}

	msgs, err := database.GetMessagesAroundTime(ctx, convID, sentAt, 5)
	if err != nil {
		t.Fatalf("GetMessagesAroundTime failed: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if len(msgs[0].Reactions) != 1 {
		t.Fatalf("expected exactly 1 reaction after idempotent add, got %+v", msgs[0].Reactions)
	}
	if msgs[0].Reactions[0].Sender != "me" || msgs[0].Reactions[0].Emoji != "👍" {
		t.Fatalf("unexpected reaction: %+v", msgs[0].Reactions[0])
	}

	// Add a second distinct reaction.
	if err := database.AddReaction(ctx, msgID, "me", "🔥"); err != nil {
		t.Fatalf("AddReaction second emoji failed: %v", err)
	}

	// Remove the first reaction; the second must survive.
	if err := database.RemoveReaction(ctx, msgID, "me", "👍"); err != nil {
		t.Fatalf("RemoveReaction failed: %v", err)
	}

	msgs, err = database.GetMessagesAroundTime(ctx, convID, sentAt, 5)
	if err != nil {
		t.Fatalf("GetMessagesAroundTime after remove failed: %v", err)
	}
	if len(msgs[0].Reactions) != 1 || msgs[0].Reactions[0].Emoji != "🔥" {
		t.Fatalf("expected only 🔥 after removing 👍, got %+v", msgs[0].Reactions)
	}

	// Remove the last reaction; removal is idempotent.
	if err := database.RemoveReaction(ctx, msgID, "me", "🔥"); err != nil {
		t.Fatalf("RemoveReaction final failed: %v", err)
	}
	if err := database.RemoveReaction(ctx, msgID, "me", "🔥"); err != nil {
		t.Fatalf("idempotent RemoveReaction failed: %v", err)
	}
	msgs, err = database.GetMessagesAroundTime(ctx, convID, sentAt, 5)
	if err != nil {
		t.Fatalf("GetMessagesAroundTime after final remove failed: %v", err)
	}
	if len(msgs[0].Reactions) != 0 {
		t.Fatalf("expected no reactions after removal, got %+v", msgs[0].Reactions)
	}

	// Non-existent message fails closed.
	if err := database.AddReaction(ctx, "missing-msg", "me", "👍"); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for AddReaction on missing message, got %v", err)
	}
	if err := database.RemoveReaction(ctx, "missing-msg", "me", "👍"); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for RemoveReaction on missing message, got %v", err)
	}

	// Empty emoji is rejected.
	if err := database.AddReaction(ctx, msgID, "me", ""); err == nil {
		t.Fatalf("expected error for empty emoji on AddReaction")
	}
}

// [Requirement(DB-02)]
func TestMessages_WindowQueryAroundTime(t *testing.T) {
	database := getTestDB(t)
	defer database.Close()

	ctx := context.Background()
	convID := fmt.Sprintf("conv-window-%d", time.Now().UnixNano())

	if err := database.UpsertConversation(ctx, &db.Conversation{
		ID:   convID,
		Name: "Window Query Test",
	}); err != nil {
		t.Fatalf("create conversation failed: %v", err)
	}

	targetTime := time.Date(2026, 11, 2, 16, 0, 0, 0, time.UTC)
	windowMinutes := 15 // Window is [15:45:00, 16:15:00]

	// Create messages at various offsets relative to targetTime
	// -20 min: outside window (15:40)
	// -15 min: exact lower boundary (15:45)
	// 0 min: center (16:00)
	// +15 min: exact upper boundary (16:15)
	// +20 min: outside window (16:20)
	offsets := []int{-20, -15, 0, 15, 20}
	for i, offset := range offsets {
		m := &db.Message{
			ID:             fmt.Sprintf("msg-win-%d-%d", time.Now().UnixNano(), i),
			ConversationID: convID,
			SenderID:       "sender",
			SenderName:     "Sender",
			SentAt:         targetTime.Add(time.Duration(offset) * time.Minute),
			Body:           fmt.Sprintf("Offset %d min", offset),
		}
		if err := database.UpsertMessage(ctx, m); err != nil {
			t.Fatalf("upsert message offset %d failed: %v", offset, err)
		}
	}

	// Query window
	results, err := database.GetMessagesAroundTime(ctx, convID, targetTime, windowMinutes)
	if err != nil {
		t.Fatalf("GetMessagesAroundTime failed: %v", err)
	}

	// Expected: exactly 3 messages (-15, 0, +15)
	if len(results) != 3 {
		t.Fatalf("expected exactly 3 messages within boundary [%v, %v], got %d",
			targetTime.Add(-15*time.Minute), targetTime.Add(15*time.Minute), len(results))
	}

	if results[0].Body != "Offset -15 min" {
		t.Errorf("expected lower boundary message 'Offset -15 min', got %q", results[0].Body)
	}
	if results[1].Body != "Offset 0 min" {
		t.Errorf("expected center message 'Offset 0 min', got %q", results[1].Body)
	}
	if results[2].Body != "Offset 15 min" {
		t.Errorf("expected upper boundary message 'Offset 15 min', got %q", results[2].Body)
	}
}

// [Requirement(DB-02)]
func TestMessages_GetUnassignedMessages(t *testing.T) {
	database := getTestDB(t)
	defer database.Close()

	ctx := context.Background()
	convID := fmt.Sprintf("conv-unassigned-%d", time.Now().UnixNano())

	if err := database.UpsertConversation(ctx, &db.Conversation{
		ID:   convID,
		Name: "Unassigned Test",
	}); err != nil {
		t.Fatalf("create conversation failed: %v", err)
	}

	baseTime := time.Date(2026, 11, 2, 18, 0, 0, 0, time.UTC)
	msg1ID := fmt.Sprintf("msg-unassigned-1-%d", time.Now().UnixNano())
	msg2ID := fmt.Sprintf("msg-unassigned-2-%d", time.Now().UnixNano())

	_ = database.UpsertMessage(ctx, &db.Message{
		ID:             msg1ID,
		ConversationID: convID,
		SenderID:       "s1",
		SenderName:     "S1",
		SentAt:         baseTime,
		Body:           "Message 1 unassigned",
	})
	_ = database.UpsertMessage(ctx, &db.Message{
		ID:             msg2ID,
		ConversationID: convID,
		SenderID:       "s1",
		SenderName:     "S1",
		SentAt:         baseTime.Add(5 * time.Minute),
		Body:           "Message 2 unassigned",
	})

	// Before session assignment: both messages unassigned
	unassigned, err := database.GetUnassignedMessages(ctx, convID)
	if err != nil {
		t.Fatalf("GetUnassignedMessages failed: %v", err)
	}
	if len(unassigned) != 2 {
		t.Fatalf("expected 2 unassigned messages, got %d", len(unassigned))
	}

	// Create and assign session to message 1
	summary := "Session test summary"
	session := &db.ConversationSession{
		ConversationID: convID,
		StartedAt:      baseTime,
		EndedAt:        baseTime.Add(1 * time.Hour),
		MessageCount:   1,
		Summary:        &summary,
		Transcript:     "transcript",
	}
	savedSession, err := database.UpsertSession(ctx, session, []string{msg1ID})
	if err != nil {
		t.Fatalf("UpsertSession failed: %v", err)
	}
	if savedSession.ID == 0 {
		t.Fatalf("expected session ID > 0")
	}

	// After session assignment: only message 2 is unassigned
	unassignedAfter, err := database.GetUnassignedMessages(ctx, convID)
	if err != nil {
		t.Fatalf("GetUnassignedMessages after session assignment failed: %v", err)
	}
	if len(unassignedAfter) != 1 {
		t.Fatalf("expected 1 unassigned message, got %d", len(unassignedAfter))
	}
	if unassignedAfter[0].ID != msg2ID {
		t.Fatalf("expected unassigned message to be %s, got %s", msg2ID, unassignedAfter[0].ID)
	}
}
