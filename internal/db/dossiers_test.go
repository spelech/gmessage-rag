package db_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/spelech/gmessage-rag/internal/db"
)

// [Requirement(DB-05)]
// [Requirement(MCP-02)]
func TestDossierVersionAuditHistory(t *testing.T) {
	database := getTestDB(t)
	defer database.Close()

	ctx := context.Background()
	convID := fmt.Sprintf("conv-dossier-%d", time.Now().UnixNano())
	contactName := "Robert Oppenheimer"

	// Create conversation first
	if err := database.UpsertConversation(ctx, &db.Conversation{
		ID:           convID,
		Name:         contactName,
		Participants: []string{"+15551112222"},
		IsTracked:    true,
	}); err != nil {
		t.Fatalf("upsert conversation failed: %v", err)
	}

	// 1. Initial Dossier Creation (Version 1)
	mdV1 := "# Living Dossier: Robert Oppenheimer\n\nProject Lead on Manhattan Project."
	stateV1 := map[string]any{
		"role":      "Director",
		"location":  "Los Alamos",
		"clearance": "Q",
	}
	t1 := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)

	d1, err := database.UpsertContactDossier(ctx, convID, contactName, mdV1, stateV1, nil, &t1)
	if err != nil {
		t.Fatalf("initial UpsertContactDossier failed: %v", err)
	}

	if d1.Version != 1 {
		t.Fatalf("expected version 1 on initial creation, got %d", d1.Version)
	}
	if d1.ContactName != contactName {
		t.Fatalf("expected contact name %q, got %q", contactName, d1.ContactName)
	}
	if d1.MarkdownContent != mdV1 {
		t.Fatalf("expected markdown %q, got %q", mdV1, d1.MarkdownContent)
	}
	if d1.LastProcessedMessageAt == nil || !d1.LastProcessedMessageAt.Equal(t1) {
		t.Fatalf("expected lastProcessedMessageAt %v, got %v", t1, d1.LastProcessedMessageAt)
	}

	// History should be empty on initial creation
	history, err := database.GetDossierHistory(ctx, convID, 10)
	if err != nil {
		t.Fatalf("GetDossierHistory failed: %v", err)
	}
	if len(history) != 0 {
		t.Fatalf("expected 0 history records on initial creation, got %d", len(history))
	}

	// 2. Second Dossier Update (Version 2)
	// Create a dummy session to act as trigger_session_id
	sumText := "Trinity test planning session"
	triggerSession := &db.ConversationSession{
		ConversationID: convID,
		StartedAt:      t1.Add(1 * time.Hour),
		EndedAt:        t1.Add(2 * time.Hour),
		MessageCount:   1,
		Summary:        &sumText,
		Transcript:     "Session transcript",
	}
	savedSession, err := database.UpsertSession(ctx, triggerSession, nil)
	if err != nil {
		t.Fatalf("UpsertSession failed: %v", err)
	}

	mdV2 := "# Living Dossier: Robert Oppenheimer\n\nDirecting Trinity test preparations."
	stateV2 := map[string]any{
		"role":     "Director",
		"location": "Jornada del Muerto",
		"status":   "Armed",
	}
	t2 := t1.Add(2 * time.Hour)

	d2, err := database.UpsertContactDossier(ctx, convID, contactName, mdV2, stateV2, &savedSession.ID, &t2)
	if err != nil {
		t.Fatalf("second UpsertContactDossier failed: %v", err)
	}

	if d2.Version != 2 {
		t.Fatalf("expected version 2, got %d", d2.Version)
	}

	// History should now have 1 record preserving Version 1 state
	history, err = database.GetDossierHistory(ctx, convID, 10)
	if err != nil {
		t.Fatalf("GetDossierHistory failed: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("expected 1 history record, got %d", len(history))
	}

	h1 := history[0]
	if h1.Version != 1 {
		t.Fatalf("expected snapshotted version 1, got %d", h1.Version)
	}
	if h1.MarkdownContent != mdV1 {
		t.Fatalf("expected snapshotted markdown %q, got %q", mdV1, h1.MarkdownContent)
	}
	if h1.StructuredState["role"] != "Director" || h1.StructuredState["clearance"] != "Q" {
		t.Fatalf("expected snapshotted state from V1, got %+v", h1.StructuredState)
	}
	if h1.TriggerSessionID == nil || *h1.TriggerSessionID != savedSession.ID {
		t.Fatalf("expected trigger_session_id %d, got %v", savedSession.ID, h1.TriggerSessionID)
	}

	// 3. Third Dossier Update (Version 3)
	mdV3 := "# Living Dossier: Robert Oppenheimer\n\nPost-Trinity analysis complete."
	stateV3 := map[string]any{
		"role":     "Director",
		"location": "Princeton IAS",
		"status":   "Complete",
	}
	t3 := t2.Add(5 * time.Hour)

	d3, err := database.UpsertContactDossier(ctx, convID, contactName, mdV3, stateV3, nil, &t3)
	if err != nil {
		t.Fatalf("third UpsertContactDossier failed: %v", err)
	}
	if d3.Version != 3 {
		t.Fatalf("expected version 3, got %d", d3.Version)
	}

	// History should now have 2 records (Version 2 first, then Version 1)
	history, err = database.GetDossierHistory(ctx, convID, 10)
	if err != nil {
		t.Fatalf("GetDossierHistory failed: %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("expected 2 history records, got %d", len(history))
	}

	if history[0].Version != 2 || history[0].MarkdownContent != mdV2 {
		t.Fatalf("expected newest snapshot to be version 2 with mdV2, got version %d", history[0].Version)
	}
	if history[1].Version != 1 || history[1].MarkdownContent != mdV1 {
		t.Fatalf("expected oldest snapshot to be version 1 with mdV1, got version %d", history[1].Version)
	}

	// Check boundary limit on GetDossierHistory (0-1-N)
	histLimit1, err := database.GetDossierHistory(ctx, convID, 1)
	if err != nil {
		t.Fatalf("GetDossierHistory limit 1 failed: %v", err)
	}
	if len(histLimit1) != 1 {
		t.Fatalf("expected 1 history record with limit=1, got %d", len(histLimit1))
	}
	if histLimit1[0].Version != 2 {
		t.Fatalf("expected version 2 in limit=1 history, got %d", histLimit1[0].Version)
	}
}

// [Requirement(DB-05)]
// [Requirement(MCP-02)]
func TestGetContactDossier_Resolution(t *testing.T) {
	database := getTestDB(t)
	defer database.Close()

	ctx := context.Background()
	convID := fmt.Sprintf("conv-resolve-%d", time.Now().UnixNano())
	contactName := "Niels Bohr"
	phone := fmt.Sprintf("+1555999%04d", time.Now().UnixNano()%10000)

	// Create conversation with participants
	if err := database.UpsertConversation(ctx, &db.Conversation{
		ID:           convID,
		Name:         "Bohr Institute Group",
		Participants: []string{phone, "+15557776666"},
		IsTracked:    true,
	}); err != nil {
		t.Fatalf("upsert conversation failed: %v", err)
	}

	now := time.Now().UTC()
	md := "# Living Dossier: Niels Bohr\n\nCopenhagen interpretation."
	state := map[string]any{"institute": "Copenhagen", "nobel": 1922}

	_, err := database.UpsertContactDossier(ctx, convID, contactName, md, state, nil, &now)
	if err != nil {
		t.Fatalf("UpsertContactDossier failed: %v", err)
	}

	// 1. Resolve via exact Conversation ID
	dByConvID, err := database.GetContactDossier(ctx, convID)
	if err != nil {
		t.Fatalf("resolve by convID failed: %v", err)
	}
	if dByConvID.ConversationID != convID || dByConvID.ContactName != contactName {
		t.Fatalf("unexpected dossier by convID: %+v", dByConvID)
	}

	// 2. Resolve via Phone Number in participants
	dByPhone, err := database.GetContactDossier(ctx, phone)
	if err != nil {
		t.Fatalf("resolve by phone failed: %v", err)
	}
	if dByPhone.ConversationID != convID || dByPhone.ContactName != contactName {
		t.Fatalf("unexpected dossier by phone: %+v", dByPhone)
	}

	// 3. Resolve via Contact Name (case-insensitive)
	dByName, err := database.GetContactDossier(ctx, "niels bohr")
	if err != nil {
		t.Fatalf("resolve by contact name failed: %v", err)
	}
	if dByName.ConversationID != convID || dByName.ContactName != contactName {
		t.Fatalf("unexpected dossier by contact name: %+v", dByName)
	}

	// 4. Resolve via Conversation Name match
	dByConvName, err := database.GetContactDossier(ctx, "Bohr Institute Group")
	if err != nil {
		t.Fatalf("resolve by conversation name failed: %v", err)
	}
	if dByConvName.ConversationID != convID {
		t.Fatalf("unexpected dossier by conv name: %+v", dByConvName)
	}

	// 5. Non-existent identifier returns ErrNotFound
	_, err = database.GetContactDossier(ctx, "non-existent-contact-identity")
	if !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for non-existent contact, got %v", err)
	}
}

// [Requirement(DB-05)]
// [Requirement(MCP-02)]
func TestGetDossierLiveTail(t *testing.T) {
	database := getTestDB(t)
	defer database.Close()

	ctx := context.Background()
	convID := fmt.Sprintf("conv-tail-%d", time.Now().UnixNano())

	if err := database.UpsertConversation(ctx, &db.Conversation{
		ID:   convID,
		Name: "Live Tail Test Thread",
	}); err != nil {
		t.Fatalf("upsert conversation failed: %v", err)
	}

	baseTime := time.Date(2026, 11, 2, 14, 0, 0, 0, time.UTC)

	// Create 5 messages spaced by 10 minutes:
	// m0: 14:00 (processed)
	// m1: 14:10 (processed, last processed at 14:10)
	// m2: 14:20 (unprocessed)
	// m3: 14:30 (unprocessed)
	// m4: 14:40 (unprocessed)
	for i := 0; i < 5; i++ {
		msg := &db.Message{
			ID:             fmt.Sprintf("msg-tail-%d-%d", time.Now().UnixNano(), i),
			ConversationID: convID,
			SenderID:       "participant-1",
			SenderName:     "Participant",
			SentAt:         baseTime.Add(time.Duration(i*10) * time.Minute),
			Body:           fmt.Sprintf("Tail message %d", i),
		}
		if err := database.UpsertMessage(ctx, msg); err != nil {
			t.Fatalf("upsert message %d failed: %v", i, err)
		}
	}

	lastProcessed := baseTime.Add(10 * time.Minute) // 14:10 (covers m0 and m1)

	// 1. Query live tail after lastProcessed (should return m2, m3, m4)
	tail, err := database.GetDossierLiveTail(ctx, convID, &lastProcessed)
	if err != nil {
		t.Fatalf("GetDossierLiveTail failed: %v", err)
	}

	if len(tail) != 3 {
		t.Fatalf("expected 3 live tail messages, got %d", len(tail))
	}

	if tail[0].Body != "Tail message 2" || tail[1].Body != "Tail message 3" || tail[2].Body != "Tail message 4" {
		t.Fatalf("unexpected tail messages: %+v", tail)
	}

	// Verify chronological ordering
	for i := 1; i < len(tail); i++ {
		if !tail[i].SentAt.After(tail[i-1].SentAt) {
			t.Fatalf("expected strictly increasing sent_at, got %v <= %v", tail[i].SentAt, tail[i-1].SentAt)
		}
	}

	// 2. Query live tail when all messages are processed (lastProcessed = 14:50)
	allProcessed := baseTime.Add(50 * time.Minute)
	emptyTail, err := database.GetDossierLiveTail(ctx, convID, &allProcessed)
	if err != nil {
		t.Fatalf("GetDossierLiveTail (all processed) failed: %v", err)
	}
	if len(emptyTail) != 0 {
		t.Fatalf("expected 0 messages when all processed, got %d", len(emptyTail))
	}

	// 3. Query live tail with nil lastProcessed (should return all 5 messages)
	allMessages, err := database.GetDossierLiveTail(ctx, convID, nil)
	if err != nil {
		t.Fatalf("GetDossierLiveTail(nil) failed: %v", err)
	}
	if len(allMessages) != 5 {
		t.Fatalf("expected all 5 messages with nil lastProcessed, got %d", len(allMessages))
	}
	if allMessages[0].Body != "Tail message 0" || allMessages[4].Body != "Tail message 4" {
		t.Fatalf("unexpected full messages list: %+v", allMessages)
	}
}
