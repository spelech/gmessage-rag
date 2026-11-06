package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"github.com/spelech/gmessage-rag/internal/db"
)

// OpenLoopStore defines the DB operations required by the OpenLoopLinker.
type OpenLoopStore interface {
	GetLinksForSession(ctx context.Context, sessionID int64) ([]db.MessageLink, error)
	InsertMessageLink(ctx context.Context, link *db.MessageLink) (*db.MessageLink, error)
	GetContactDossier(ctx context.Context, contactIdentifier string) (*db.ContactDossier, error)
}

// OpenLoopCandidate holds an open loop from prior dossier state.
type OpenLoopCandidate struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	RelativeDate string `json:"relative_date,omitempty"`
	Date         string `json:"date,omitempty"`
	Status       string `json:"status,omitempty"`
	Details      string `json:"details,omitempty"`
	SessionID    int64  `json:"session_id,omitempty"`
}

// OpenLoopMatch holds an antecedent link detected between a session and an open loop.
type OpenLoopMatch struct {
	OpenLoopID     string  `json:"open_loop_id"`
	TargetSessionID int64  `json:"target_session_id"`
	Reason         string  `json:"reason"`
	Confidence     float64 `json:"confidence"`
	Status         string  `json:"status"` // "resolved" or "continuation"
}

// OpenLoopLinker detects when a new session references, answers, or resolves an active open loop.
type OpenLoopLinker struct {
	store     OpenLoopStore
	llmClient LLMClient
	logger    zerolog.Logger
}

// NewOpenLoopLinker constructs an OpenLoopLinker.
func NewOpenLoopLinker(store OpenLoopStore, llm LLMClient, logger zerolog.Logger) *OpenLoopLinker {
	return &OpenLoopLinker{
		store:     store,
		llmClient: llm,
		logger:    logger,
	}
}

// ExtractActiveOpenLoops pulls currently open (unresolved) loops from dossier structured state.
func ExtractActiveOpenLoops(dossier *db.ContactDossier) []OpenLoopCandidate {
	if dossier == nil || dossier.StructuredState == nil {
		return nil
	}
	raw, ok := dossier.StructuredState["open_loops"]
	if !ok {
		return nil
	}
	items := asMapSlice(raw)
	var active []OpenLoopCandidate
	for _, item := range items {
		status, _ := item["status"].(string)
		status = strings.ToLower(strings.TrimSpace(status))
		if status == "resolved" || status == "closed" || status == "cancelled" {
			continue
		}
		id, _ := item["id"].(string)
		title, _ := item["title"].(string)
		if strings.TrimSpace(title) == "" {
			title, _ = item["name"].(string)
		}
		if strings.TrimSpace(title) == "" && strings.TrimSpace(id) == "" {
			continue
		}
		rel, _ := item["relative_date"].(string)
		date, _ := item["date"].(string)
		det, _ := item["details"].(string)

		var sessID int64
		if sid, ok := item["session_id"].(float64); ok {
			sessID = int64(sid)
		}

		active = append(active, OpenLoopCandidate{
			ID:           id,
			Title:        title,
			RelativeDate: rel,
			Date:         date,
			Status:       status,
			Details:      det,
			SessionID:    sessID,
		})
	}
	return active
}

const openLoopMatchSystemPrompt = `You are an antecedent resolution engine for a personal messaging thread.
Given a list of ACTIVE OPEN LOOPS from prior conversations, determine if the NEW SESSION transcript
references, updates, answers, or resolves any of them.

Rules:
1. Return ONLY a JSON array of matches. Do not wrap in markdown fences.
2. If no open loops are referenced, return empty array [].
3. For each match, provide:
   - "open_loop_id": string id matching one of the candidate open loops
   - "target_session_id": integer session id from candidate if known, else 0
   - "reason": concise explanation of how this session connects to that open loop
   - "confidence": number between 0.60 and 1.00
   - "status": "resolved" if the open loop is completed/answered, or "continuation" if still ongoing
`

// DetectAndRecordLinks checks if a session connects to prior open loops and records edges.
func (l *OpenLoopLinker) DetectAndRecordLinks(ctx context.Context, conversationID string, session *db.ConversationSession) ([]db.MessageLink, error) {
	if l.store == nil || session == nil || session.ID == 0 {
		return nil, nil
	}

	dossier, err := l.store.GetContactDossier(ctx, conversationID)
	if err != nil || dossier == nil {
		return nil, nil
	}

	activeLoops := ExtractActiveOpenLoops(dossier)
	if len(activeLoops) == 0 {
		return nil, nil
	}

	// Filter down to candidate loops that specify a valid historical target session
	var candidates []OpenLoopCandidate
	for _, loop := range activeLoops {
		if loop.SessionID > 0 && loop.SessionID != session.ID {
			candidates = append(candidates, loop)
		}
	}
	if len(candidates) == 0 {
		return nil, nil
	}

	// If no LLM client configured, perform lightweight lexical match on title/details
	var matches []OpenLoopMatch
	if l.llmClient == nil {
		transcriptLower := strings.ToLower(session.Transcript)
		for _, c := range candidates {
			if strings.TrimSpace(c.Title) != "" && strings.Contains(transcriptLower, strings.ToLower(c.Title)) {
				matches = append(matches, OpenLoopMatch{
					OpenLoopID:     c.ID,
					TargetSessionID: c.SessionID,
					Reason:         fmt.Sprintf("Transcript explicitly mentions '%s'", c.Title),
					Confidence:     0.85,
					Status:         "continuation",
				})
			}
		}
	} else {
		// LLM-assisted antecedent resolution across the small bounded candidate list
		loopsJSON, _ := json.Marshal(candidates)
		prompt := fmt.Sprintf("ACTIVE OPEN LOOPS:\n%s\n\nNEW SESSION TRANSCRIPT (Session %d):\n%s",
			string(loopsJSON), session.ID, session.Transcript)

		_, reply, err := l.llmClient.GenerateSummary(ctx, prompt)
		if err == nil && strings.TrimSpace(reply) != "" {
			cleaned := strings.TrimSpace(reply)
			cleaned = strings.TrimPrefix(cleaned, "```json")
			cleaned = strings.TrimPrefix(cleaned, "```")
			cleaned = strings.TrimSuffix(cleaned, "```")
			_ = json.Unmarshal([]byte(cleaned), &matches)
		}
	}

	var recorded []db.MessageLink
	for _, m := range matches {
		if m.TargetSessionID == 0 || m.TargetSessionID == session.ID {
			continue
		}
		linkType := "antecedent_continuation"
		if m.Status == "resolved" {
			linkType = "open_loop_resolution"
		}
		link := &db.MessageLink{
			ConversationID:  conversationID,
			SourceSessionID: session.ID,
			TargetSessionID: m.TargetSessionID,
			LinkType:        linkType,
			Confidence:      m.Confidence,
			Evidence: map[string]any{
				"open_loop_id": m.OpenLoopID,
				"reason":       m.Reason,
				"detected_at":  time.Now().UTC().Format(time.RFC3339),
			},
			Status:    "detected",
			CreatedBy: "detector:open_loops",
		}
		saved, err := l.store.InsertMessageLink(ctx, link)
		if err == nil && saved != nil {
			recorded = append(recorded, *saved)
		}
	}

	return recorded, nil
}
