package db_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/spelech/gmessage-rag/internal/db"
)

// [Requirement(DB-06)]
func TestInferDeliveryReceipts(t *testing.T) {
	database := getTestDB(t)
	defer database.Close()

	ctx := context.Background()
	convID := fmt.Sprintf("conv-receipts-%d", time.Now().UnixNano())

	now := time.Now().UTC().Truncate(time.Microsecond)
	conv := &db.Conversation{
		ID:           convID,
		Name:         "Receipt Contact",
		Participants: []string{"+15551234567", "+15559876543"},
		IsGroup:      false,
		IsTracked:    true,
	}
	if err := database.UpsertConversation(ctx, conv); err != nil {
		t.Fatalf("failed to insert conversation: %v", err)
	}

	replySentAt := now.Add(-10 * time.Minute)

	// Status values for test cases:
	// msg1: outgoing, sent before replySentAt, raw_status = NULL -> should be updated
	// msg2: outgoing, sent before replySentAt, raw_status = 1 (pending) -> should be updated
	// msg3: incoming, sent before replySentAt, raw_status = NULL -> should NOT be updated (is_from_me = false)
	// msg4: outgoing, sent exactly at replySentAt, raw_status = 2 -> should be updated
	// msg5: outgoing, sent after replySentAt, raw_status = NULL -> should NOT be updated (strict time boundary)
	// msg6: outgoing, sent before replySentAt, raw_status = 100 (explicit carrier delivered) -> should NOT be updated (preserve explicit)
	// msg7: outgoing, sent before replySentAt, raw_status = 105 (explicit read) -> should NOT be updated (preserve explicit)

	rawStatusPending := int32(1)
	rawStatusSent := int32(2)
	rawStatusDeliveredCarrier := int32(100)
	rawStatusReadCarrier := int32(105)

	msgs := []*db.Message{
		{
			ID:             fmt.Sprintf("msg-out-before-null-%d", time.Now().UnixNano()),
			ConversationID: convID,
			SenderID:       "me",
			SenderName:     "Me",
			IsFromMe:       true,
			SentAt:         replySentAt.Add(-5 * time.Minute),
			Body:           "Hello there",
			RawStatus:      nil,
		},
		{
			ID:             fmt.Sprintf("msg-out-before-pending-%d", time.Now().UnixNano()),
			ConversationID: convID,
			SenderID:       "me",
			SenderName:     "Me",
			IsFromMe:       true,
			SentAt:         replySentAt.Add(-4 * time.Minute),
			Body:           "How are you?",
			RawStatus:      &rawStatusPending,
		},
		{
			ID:             fmt.Sprintf("msg-in-before-%d", time.Now().UnixNano()),
			ConversationID: convID,
			SenderID:       "+15551234567",
			SenderName:     "Receipt Contact",
			IsFromMe:       false,
			SentAt:         replySentAt.Add(-3 * time.Minute),
			Body:           "Incoming message",
			RawStatus:      nil,
		},
		{
			ID:             fmt.Sprintf("msg-out-exact-%d", time.Now().UnixNano()),
			ConversationID: convID,
			SenderID:       "me",
			SenderName:     "Me",
			IsFromMe:       true,
			SentAt:         replySentAt,
			Body:           "Right on boundary",
			RawStatus:      &rawStatusSent,
		},
		{
			ID:             fmt.Sprintf("msg-out-after-%d", time.Now().UnixNano()),
			ConversationID: convID,
			SenderID:       "me",
			SenderName:     "Me",
			IsFromMe:       true,
			SentAt:         replySentAt.Add(2 * time.Minute),
			Body:           "Sent after reply",
			RawStatus:      nil,
		},
		{
			ID:             fmt.Sprintf("msg-out-explicit-100-%d", time.Now().UnixNano()),
			ConversationID: convID,
			SenderID:       "me",
			SenderName:     "Me",
			IsFromMe:       true,
			SentAt:         replySentAt.Add(-6 * time.Minute),
			Body:           "Explicit delivery message",
			RawStatus:      &rawStatusDeliveredCarrier,
		},
		{
			ID:             fmt.Sprintf("msg-out-explicit-105-%d", time.Now().UnixNano()),
			ConversationID: convID,
			SenderID:       "me",
			SenderName:     "Me",
			IsFromMe:       true,
			SentAt:         replySentAt.Add(-2 * time.Minute),
			Body:           "Explicit read message",
			RawStatus:      &rawStatusReadCarrier,
		},
	}

	if _, err := database.UpsertMessagesBatch(ctx, msgs); err != nil {
		t.Fatalf("failed to insert batch messages: %v", err)
	}

	// 1. Check initial pending unacknowledged outgoing messages
	pendingBefore, err := database.GetPendingUnacknowledgedMessages(ctx, convID)
	if err != nil {
		t.Fatalf("GetPendingUnacknowledgedMessages failed: %v", err)
	}

	// msgs[0], msgs[1], msgs[3], msgs[4] should be pending (4 messages)
	if len(pendingBefore) != 4 {
		t.Fatalf("expected 4 pending unacknowledged messages before inference, got %d", len(pendingBefore))
	}

	// Verify order: sent_at ASC
	for i := 1; i < len(pendingBefore); i++ {
		if pendingBefore[i].SentAt.Before(pendingBefore[i-1].SentAt) {
			t.Fatalf("expected pending messages in sent_at ASC order, but index %d (%v) is before index %d (%v)",
				i, pendingBefore[i].SentAt, i-1, pendingBefore[i-1].SentAt)
		}
	}

	// 2. Perform InferDeliveryReceipts
	updatedCount, err := database.InferDeliveryReceipts(ctx, convID, replySentAt)
	if err != nil {
		t.Fatalf("InferDeliveryReceipts failed: %v", err)
	}

	// msgs[0], msgs[1], and msgs[3] should be updated (3 messages)
	// msgs[4] is sent after replySentAt (strict time boundary)
	// msgs[2] is incoming
	// msgs[5] and msgs[6] are already >= 100
	if updatedCount != 3 {
		t.Fatalf("expected 3 updated messages, got %d", updatedCount)
	}

	// 3. Idempotency test: repeating inference with same timestamp must update 0 rows
	updatedSecondRun, err := database.InferDeliveryReceipts(ctx, convID, replySentAt)
	if err != nil {
		t.Fatalf("second InferDeliveryReceipts failed: %v", err)
	}
	if updatedSecondRun != 0 {
		t.Fatalf("expected 0 updated messages on second run, got %d", updatedSecondRun)
	}

	// 4. Verify post-inference state
	pendingAfter, err := database.GetPendingUnacknowledgedMessages(ctx, convID)
	if err != nil {
		t.Fatalf("GetPendingUnacknowledgedMessages after inference failed: %v", err)
	}

	// Only msgs[4] (sent after replySentAt) should remain unacknowledged
	if len(pendingAfter) != 1 {
		t.Fatalf("expected 1 pending unacknowledged message remaining, got %d", len(pendingAfter))
	}
	if pendingAfter[0].ID != msgs[4].ID {
		t.Fatalf("expected remaining message ID %s, got %s", msgs[4].ID, pendingAfter[0].ID)
	}

	// Retrieve all messages around the time to inspect exact statuses
	allMsgs, err := database.GetMessagesAroundTime(ctx, convID, replySentAt, 60)
	if err != nil {
		t.Fatalf("GetMessagesAroundTime failed: %v", err)
	}

	msgMap := make(map[string]db.Message)
	for _, m := range allMsgs {
		msgMap[m.ID] = m
	}

	// Verify msg0: updated to BugleStatusDeliveredInferred (108)
	if m, ok := msgMap[msgs[0].ID]; !ok || m.RawStatus == nil || *m.RawStatus != db.BugleStatusDeliveredInferred {
		t.Errorf("msg0 expected status %d, got %v", db.BugleStatusDeliveredInferred, m.RawStatus)
	}

	// Verify msg1: updated to BugleStatusDeliveredInferred (108)
	if m, ok := msgMap[msgs[1].ID]; !ok || m.RawStatus == nil || *m.RawStatus != db.BugleStatusDeliveredInferred {
		t.Errorf("msg1 expected status %d, got %v", db.BugleStatusDeliveredInferred, m.RawStatus)
	}

	// Verify msg2 (incoming): status should remain nil
	if m, ok := msgMap[msgs[2].ID]; !ok || m.RawStatus != nil {
		t.Errorf("msg2 (incoming) expected nil status, got %v", m.RawStatus)
	}

	// Verify msg3 (exact timestamp): updated to BugleStatusDeliveredInferred (108)
	if m, ok := msgMap[msgs[3].ID]; !ok || m.RawStatus == nil || *m.RawStatus != db.BugleStatusDeliveredInferred {
		t.Errorf("msg3 expected status %d, got %v", db.BugleStatusDeliveredInferred, m.RawStatus)
	}

	// Verify msg4 (after replySentAt): status should remain nil
	if m, ok := msgMap[msgs[4].ID]; !ok || m.RawStatus != nil {
		t.Errorf("msg4 (after replySentAt) expected nil status, got %v", m.RawStatus)
	}

	// Verify msg5 (explicit 100): preserved
	if m, ok := msgMap[msgs[5].ID]; !ok || m.RawStatus == nil || *m.RawStatus != 100 {
		t.Errorf("msg5 expected preserved status 100, got %v", m.RawStatus)
	}

	// Verify msg6 (explicit 105): preserved
	if m, ok := msgMap[msgs[6].ID]; !ok || m.RawStatus == nil || *m.RawStatus != 105 {
		t.Errorf("msg6 expected preserved status 105, got %v", m.RawStatus)
	}
}

// [Requirement(DB-06)]
func TestGetPendingUnacknowledgedMessages_01NBoundaries(t *testing.T) {
	database := getTestDB(t)
	defer database.Close()

	ctx := context.Background()
	convID := fmt.Sprintf("conv-pending-01n-%d", time.Now().UnixNano())

	conv := &db.Conversation{
		ID:           convID,
		Name:         "01N Boundaries Contact",
		Participants: []string{"+15550009999"},
		IsGroup:      false,
		IsTracked:    true,
	}
	if err := database.UpsertConversation(ctx, conv); err != nil {
		t.Fatalf("failed to insert conversation: %v", err)
	}

	// 0-Boundary: Empty conversation
	pending0, err := database.GetPendingUnacknowledgedMessages(ctx, convID)
	if err != nil {
		t.Fatalf("GetPendingUnacknowledgedMessages (0) failed: %v", err)
	}
	if len(pending0) != 0 {
		t.Fatalf("expected 0 pending messages, got %d", len(pending0))
	}

	now := time.Now().UTC()

	// 1-Boundary: Single unacknowledged message
	msgSingle := &db.Message{
		ID:             fmt.Sprintf("msg-single-%d", time.Now().UnixNano()),
		ConversationID: convID,
		SenderID:       "me",
		SenderName:     "Me",
		IsFromMe:       true,
		SentAt:         now,
		Body:           "Single outgoing message",
		RawStatus:      nil,
	}
	if err := database.UpsertMessage(ctx, msgSingle); err != nil {
		t.Fatalf("failed to insert single message: %v", err)
	}

	pending1, err := database.GetPendingUnacknowledgedMessages(ctx, convID)
	if err != nil {
		t.Fatalf("GetPendingUnacknowledgedMessages (1) failed: %v", err)
	}
	if len(pending1) != 1 {
		t.Fatalf("expected 1 pending message, got %d", len(pending1))
	}
	if pending1[0].ID != msgSingle.ID {
		t.Fatalf("expected pending message ID %s, got %s", msgSingle.ID, pending1[0].ID)
	}

	// N-Boundary: Multiple unacknowledged messages
	batchN := make([]*db.Message, 5)
	for i := 0; i < 5; i++ {
		batchN[i] = &db.Message{
			ID:             fmt.Sprintf("msg-n-%d-%d", i, time.Now().UnixNano()),
			ConversationID: convID,
			SenderID:       "me",
			SenderName:     "Me",
			IsFromMe:       true,
			SentAt:         now.Add(time.Duration(i+1) * time.Minute),
			Body:           fmt.Sprintf("Batch N outgoing message #%d", i),
			RawStatus:      nil,
		}
	}
	if _, err := database.UpsertMessagesBatch(ctx, batchN); err != nil {
		t.Fatalf("failed to insert batch N: %v", err)
	}

	pendingN, err := database.GetPendingUnacknowledgedMessages(ctx, convID)
	if err != nil {
		t.Fatalf("GetPendingUnacknowledgedMessages (N) failed: %v", err)
	}
	if len(pendingN) != 6 { // 1 + 5 = 6
		t.Fatalf("expected 6 pending messages, got %d", len(pendingN))
	}

	// Infer all up to now + 10m
	updated, err := database.InferDeliveryReceipts(ctx, convID, now.Add(10*time.Minute))
	if err != nil {
		t.Fatalf("InferDeliveryReceipts failed: %v", err)
	}
	if updated != 6 {
		t.Fatalf("expected 6 updated, got %d", updated)
	}

	pendingFinal, err := database.GetPendingUnacknowledgedMessages(ctx, convID)
	if err != nil {
		t.Fatalf("GetPendingUnacknowledgedMessages (final) failed: %v", err)
	}
	if len(pendingFinal) != 0 {
		t.Fatalf("expected 0 pending messages after acknowledging all, got %d", len(pendingFinal))
	}
}
