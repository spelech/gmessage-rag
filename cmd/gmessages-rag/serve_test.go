package main

import (
	"context"
	"testing"

	"github.com/rs/zerolog"
	"github.com/spelech/gmessage-rag/internal/db"
	"github.com/stretchr/testify/assert"
)

func TestCleanDigits(t *testing.T) {
	assert.Equal(t, "1234567890", cleanDigits("(123) 456-7890"))
	assert.Equal(t, "123", cleanDigits("+1 2 3 abc"))
	assert.Equal(t, "", cleanDigits("abc-def!"))
}

func TestResolveTrackedConversations_NilClient(t *testing.T) {
	logger := zerolog.Nop()
	ctx := context.Background()

	assert.Nil(t, resolveTrackedConversations(ctx, logger, nil, nil))
	assert.Nil(t, resolveTrackedConversations(ctx, logger, nil, []string{"contact-1"}))
}

func TestDispatchDossierWebhook_NilOrDisabled(t *testing.T) {
	logger := zerolog.Nop()
	ctx := context.Background()

	// Should not panic on nil dispatcher or nil dossier
	dispatchDossierWebhook(ctx, logger, nil, nil)
	dispatchDossierWebhook(ctx, logger, nil, &db.ContactDossier{ConversationID: "12"})
}
