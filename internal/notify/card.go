package notify

import (
	"fmt"
	"strings"

	"github.com/spelech/gmessage-rag/internal/db"
)

// SlackBlock represents a generic Slack Block Kit element.
type SlackBlock map[string]any

// SlackDossierCard builds a structured Slack Block Kit message payload for a Living Dossier.
// Requirement ID: TRANS-04
func SlackDossierCard(dossier *db.ContactDossier) map[string]any {
	if dossier == nil {
		return map[string]any{"blocks": []any{}}
	}

	title := strings.TrimSpace(dossier.ContactName)
	if title == "" {
		title = strings.TrimSpace(dossier.ConversationID)
	}
	if title == "" {
		title = "Unknown Contact"
	}

	var blocks []SlackBlock

	// 1. Header block
	blocks = append(blocks, SlackBlock{
		"type": "header",
		"text": map[string]any{
			"type":  "plain_text",
			"text":  fmt.Sprintf("📋 Living Dossier: %s", title),
			"emoji": true,
		},
	})

	state := dossier.StructuredState
	if state == nil {
		state = make(map[string]any)
	}

	// 2. Metadata context / fields block
	var contactFields []map[string]any
	contactFields = append(contactFields, map[string]any{
		"type": "mrkdwn",
		"text": fmt.Sprintf("*Conversation ID:*\n`%s`", dossier.ConversationID),
	})
	contactFields = append(contactFields, map[string]any{
		"type": "mrkdwn",
		"text": fmt.Sprintf("*Dossier Version:*\nv%d", dossier.Version),
	})

	if contact, ok := state["contact"].(map[string]any); ok {
		if nick, _ := contact["nickname"].(string); strings.TrimSpace(nick) != "" {
			contactFields = append(contactFields, map[string]any{
				"type": "mrkdwn",
				"text": fmt.Sprintf("*Nickname:*\n%s", nick),
			})
		}
		if avatar, _ := contact["avatar_hex_color"].(string); strings.TrimSpace(avatar) != "" {
			contactFields = append(contactFields, map[string]any{
				"type": "mrkdwn",
				"text": fmt.Sprintf("*Avatar Color:*\n`%s`", avatar),
			})
		}
	}

	blocks = append(blocks, SlackBlock{
		"type":   "section",
		"fields": contactFields,
	})

	blocks = append(blocks, SlackBlock{"type": "divider"})

	// 3. Profile Overview
	if profile, ok := state["profile"].(map[string]any); ok && len(profile) > 0 {
		var profLines []string
		for k, v := range profile {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				profLines = append(profLines, fmt.Sprintf("• *%s:* %s", strings.Title(k), s))
			}
		}
		if len(profLines) > 0 {
			blocks = append(blocks, SlackBlock{
				"type": "section",
				"text": map[string]any{
					"type": "mrkdwn",
					"text": "*Overview:*\n" + strings.Join(profLines, "\n"),
				},
			})
		}
	}

	// 4. Upcoming Events
	if items := extractCardItems(state["upcoming_events"]); len(items) > 0 {
		var lines []string
		for _, it := range items {
			line := fmt.Sprintf("• *%s*", it.Title)
			if it.Date != "" {
				line += fmt.Sprintf(" — `%s`", it.Date)
			}
			if it.Details != "" {
				line += fmt.Sprintf(" (%s)", it.Details)
			}
			lines = append(lines, line)
		}
		blocks = append(blocks, SlackBlock{
			"type": "section",
			"text": map[string]any{
				"type": "mrkdwn",
				"text": "🗓️ *Upcoming Events:*\n" + strings.Join(lines, "\n"),
			},
		})
	}

	// 5. Open Loops
	if items := extractCardItems(state["open_loops"]); len(items) > 0 {
		var lines []string
		for _, it := range items {
			line := fmt.Sprintf("• %s", it.Title)
			if it.Date != "" {
				line += fmt.Sprintf(" — `%s`", it.Date)
			}
			lines = append(lines, line)
		}
		blocks = append(blocks, SlackBlock{
			"type": "section",
			"text": map[string]any{
				"type": "mrkdwn",
				"text": "⏳ *Open Loops:*\n" + strings.Join(lines, "\n"),
			},
		})
	}

	// 6. Footer context
	updatedStr := dossier.UpdatedAt.UTC().Format("Jan 02, 2006 15:04 UTC")
	blocks = append(blocks, SlackBlock{
		"type": "context",
		"elements": []any{
			map[string]any{
				"type": "mrkdwn",
				"text": fmt.Sprintf("Last reconciled: %s | gmessages-rag living dossier", updatedStr),
			},
		},
	})

	return map[string]any{
		"text":   fmt.Sprintf("Living Dossier: %s (v%d)", title, dossier.Version),
		"blocks": blocks,
	}
}

// AdaptiveDossierCard builds a Microsoft Adaptive Card (v1.5) payload for a Living Dossier.
// Requirement ID: TRANS-04
func AdaptiveDossierCard(dossier *db.ContactDossier) map[string]any {
	if dossier == nil {
		return map[string]any{
			"type":    "AdaptiveCard",
			"version": "1.5",
			"body":    []any{},
		}
	}

	title := strings.TrimSpace(dossier.ContactName)
	if title == "" {
		title = strings.TrimSpace(dossier.ConversationID)
	}
	if title == "" {
		title = "Unknown Contact"
	}

	var body []map[string]any

	// Header container
	body = append(body, map[string]any{
		"type":   "TextBlock",
		"text":   fmt.Sprintf("Living Dossier: %s", title),
		"size":   "Large",
		"weight": "Bolder",
		"color":  "Accent",
	})

	// Subtitle
	body = append(body, map[string]any{
		"type":     "TextBlock",
		"text":     fmt.Sprintf("Conversation: %s | Version: %d", dossier.ConversationID, dossier.Version),
		"spacing":  "None",
		"isSubtle": true,
	})

	state := dossier.StructuredState
	if state == nil {
		state = make(map[string]any)
	}

	// Upcoming events container
	if items := extractCardItems(state["upcoming_events"]); len(items) > 0 {
		var facts []map[string]any
		for _, it := range items {
			val := it.Title
			if it.Details != "" {
				val += " (" + it.Details + ")"
			}
			facts = append(facts, map[string]any{
				"title": it.Date,
				"value": val,
			})
		}
		body = append(body, map[string]any{
			"type":      "TextBlock",
			"text":      "Upcoming Events",
			"weight":    "Bolder",
			"separator": true,
		})
		body = append(body, map[string]any{
			"type":  "FactSet",
			"facts": facts,
		})
	}

	// Open loops container
	if items := extractCardItems(state["open_loops"]); len(items) > 0 {
		var facts []map[string]any
		for _, it := range items {
			titleText := it.Date
			if titleText == "" {
				titleText = "Open"
			}
			facts = append(facts, map[string]any{
				"title": titleText,
				"value": it.Title,
			})
		}
		body = append(body, map[string]any{
			"type":      "TextBlock",
			"text":      "Open Loops",
			"weight":    "Bolder",
			"separator": true,
		})
		body = append(body, map[string]any{
			"type":  "FactSet",
			"facts": facts,
		})
	}

	return map[string]any{
		"type":    "AdaptiveCard",
		"$schema": "http://adaptivecards.io/schemas/adaptive-card.json",
		"version": "1.5",
		"body":    body,
	}
}

type cardItem struct {
	Title   string
	Date    string
	Details string
}

func extractCardItems(raw any) []cardItem {
	slice, ok := raw.([]any)
	if !ok {
		return nil
	}
	var out []cardItem
	for _, item := range slice {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		title, _ := m["title"].(string)
		if title == "" {
			title, _ = m["name"].(string)
		}
		if title == "" {
			continue
		}
		date, _ := m["date"].(string)
		details, _ := m["details"].(string)
		out = append(out, cardItem{
			Title:   title,
			Date:    date,
			Details: details,
		})
	}
	return out
}
