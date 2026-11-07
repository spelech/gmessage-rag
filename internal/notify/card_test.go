package notify_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/spelech/gmessage-rag/internal/db"
	"github.com/spelech/gmessage-rag/internal/notify"
	"github.com/stretchr/testify/require"
)

// Requirement ID: TRANS-04
func TestSlackDossierCard_Rendering(t *testing.T) {
	dossier := &db.ContactDossier{
		ConversationID:  "conv-test-1",
		ContactName:     "Molly Madden",
		Version:         5,
		UpdatedAt:       time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC),
		MarkdownContent: "# Narrative",
		StructuredState: map[string]any{
			"contact": map[string]any{
				"nickname":         "Molls",
				"avatar_hex_color": "#AF5CF7",
			},
			"profile": map[string]any{
				"location": "Chicago, IL",
				"pet":      "Lula (dog)",
			},
			"upcoming_events": []any{
				map[string]any{
					"title":   "Notre Dame vs Miami Game",
					"date":    "2026-11-07",
					"details": "South Bend trip with friends",
				},
			},
			"open_loops": []any{
				map[string]any{
					"title": "Coordinate Sunday hangout",
					"date":  "2026-11-15",
				},
			},
		},
	}

	card := notify.SlackDossierCard(dossier)
	require.NotNil(t, card)

	data, err := json.Marshal(card)
	require.NoError(t, err)

	jsonStr := string(data)
	require.Contains(t, jsonStr, "Living Dossier: Molly Madden")
	require.Contains(t, jsonStr, "conv-test-1")
	require.Contains(t, jsonStr, "Molls")
	require.Contains(t, jsonStr, "#AF5CF7")
	require.Contains(t, jsonStr, "Notre Dame vs Miami Game")
	require.Contains(t, jsonStr, "Coordinate Sunday hangout")

	// Nil safety
	nilCard := notify.SlackDossierCard(nil)
	require.NotNil(t, nilCard)
	require.Empty(t, nilCard["blocks"])
}

// Requirement ID: TRANS-04
func TestAdaptiveDossierCard_Rendering(t *testing.T) {
	dossier := &db.ContactDossier{
		ConversationID:  "conv-test-2",
		ContactName:     "Steven Pelech",
		Version:         3,
		UpdatedAt:       time.Date(2026, 11, 6, 15, 0, 0, 0, time.UTC),
		MarkdownContent: "# Narrative",
		StructuredState: map[string]any{
			"upcoming_events": []any{
				map[string]any{
					"title": "Dave Matthews Band Concert",
					"date":  "2026-11-07",
				},
			},
			"open_loops": []any{
				map[string]any{
					"title": "Deploy message links graph",
				},
			},
		},
	}

	card := notify.AdaptiveDossierCard(dossier)
	require.NotNil(t, card)

	data, err := json.Marshal(card)
	require.NoError(t, err)

	jsonStr := string(data)
	require.Contains(t, jsonStr, "AdaptiveCard")
	require.Contains(t, jsonStr, "Living Dossier: Steven Pelech")
	require.Contains(t, jsonStr, "Dave Matthews Band Concert")
	require.Contains(t, jsonStr, "Deploy message links graph")

	// Nil safety
	nilCard := notify.AdaptiveDossierCard(nil)
	require.NotNil(t, nilCard)
	require.Equal(t, "AdaptiveCard", nilCard["type"])
}
