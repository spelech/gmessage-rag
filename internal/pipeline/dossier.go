package pipeline

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"github.com/spelech/gmessage-rag/internal/db"
	"github.com/spelech/gmessage-rag/internal/gm"
)

// ContactSourceFunc returns the normalized Google Contacts roster used to enrich dossiers (PIPE-04).
type ContactSourceFunc func(ctx context.Context) ([]gm.ContactSummary, error)

// DossierStore defines the persistence methods required by the DossierReconciler.
type DossierStore interface {
	GetContactDossier(ctx context.Context, contactIdentifier string) (*db.ContactDossier, error)
	UpsertContactDossier(
		ctx context.Context,
		conversationID string,
		contactName string,
		markdown string,
		structuredState map[string]any,
		triggerSessionID *int64,
		lastProcessedMessageAt *time.Time,
	) (*db.ContactDossier, error)
	GetSessionsSince(ctx context.Context, conversationID string, since *time.Time) ([]db.ConversationSession, error)
	GetConversation(ctx context.Context, id string) (*db.Conversation, error)
	GetAntecedentSessions(ctx context.Context, sourceSessionIDs []int64) ([]db.ConversationSession, error)
}

// DossierModel abstracts the injectable, OpenAI-compatible LLM reconciliation boundary.
type DossierModel interface {
	Reconcile(ctx context.Context, req DossierReconcileRequest) (*DossierReconcileResult, error)
}

// DossierReconcileRequest hands the prior dossier and new session digests to the model.
type DossierReconcileRequest struct {
	ContactName      string
	PriorMarkdown    string
	PriorState       map[string]any
	SessionDigest    string
	AntecedentDigest string // Linked historical antecedent sessions resolved via open loops or replies
	AnchorDate       time.Time
}

// DossierReconcileResult is the model's proposed reconciled dossier.
type DossierReconcileResult struct {
	Markdown        string
	StructuredState map[string]any
	Supersedes      []string
}

// DossierReconcilerConfig configures a DossierReconciler.
type DossierReconcilerConfig struct {
	Store         DossierStore
	Model         DossierModel
	ContactSource ContactSourceFunc
	OutputDir     string
	Location      *time.Location
	Logger        zerolog.Logger
}

// DossierReconciler reconciles new conversation sessions against the current Living Dossier,
// anchoring relative dates, applying chronological overrides, transitioning past events,
// enriching with Google Contacts, and exporting Markdown (PIPE-03 / PIPE-04).
type DossierReconciler struct {
	store         DossierStore
	model         DossierModel
	contactSource ContactSourceFunc
	outputDir     string
	location      *time.Location
	logger        zerolog.Logger
}

// DossierOutcome reports the result of a reconciliation pass.
type DossierOutcome struct {
	Dossier      *db.ContactDossier
	MarkdownPath string
	Changed      bool
}

// NewDossierReconciler constructs a DossierReconciler.
func NewDossierReconciler(cfg DossierReconcilerConfig) *DossierReconciler {
	loc := cfg.Location
	if loc == nil {
		loc = time.UTC
	}
	return &DossierReconciler{
		store:         cfg.Store,
		model:         cfg.Model,
		contactSource: cfg.ContactSource,
		outputDir:     cfg.OutputDir,
		location:      loc,
		logger:        cfg.Logger,
	}
}

// Reconcile folds every session newer than the dossier's last processed message into an updated
// dossier. It no-ops when nothing new has arrived. The prior dossier is snapshotted into
// contact_dossier_history by UpsertContactDossier, and the rendered Markdown is exported to disk.
func (r *DossierReconciler) Reconcile(ctx context.Context, conversationID string) (*DossierOutcome, error) {
	prior, err := r.loadPriorDossier(ctx, conversationID)
	if err != nil {
		return nil, err
	}
	var since *time.Time
	if prior != nil {
		since = prior.LastProcessedMessageAt
	}
	return r.ReconcileSince(ctx, conversationID, since)
}

// ReconcileSince reconciles sessions occurring since the given timestamp. If since is nil,
// it processes all sessions for the conversation (full rebuild).
func (r *DossierReconciler) ReconcileSince(ctx context.Context, conversationID string, since *time.Time) (*DossierOutcome, error) {
	if r.store == nil {
		return nil, errors.New("dossier store cannot be nil")
	}
	if conversationID == "" {
		return nil, errors.New("conversationID cannot be empty")
	}

	prior, err := r.loadPriorDossier(ctx, conversationID)
	if err != nil {
		return nil, err
	}

	contactName, participants, err := r.loadConversationContext(ctx, conversationID)
	if err != nil {
		return nil, err
	}

	sessions, err := r.store.GetSessionsSince(ctx, conversationID, since)
	if err != nil {
		return nil, fmt.Errorf("load sessions since: %w", err)
	}
	if len(sessions) == 0 {
		return &DossierOutcome{Dossier: prior, Changed: false}, nil
	}

	latest := sessions[len(sessions)-1]
	anchor := latest.EndedAt.UTC()

	var sessionIDs []int64
	for _, s := range sessions {
		sessionIDs = append(sessionIDs, s.ID)
	}

	var antecedentDigest string
	antecedents, err := r.store.GetAntecedentSessions(ctx, sessionIDs)
	if err == nil && len(antecedents) > 0 {
		antecedentDigest = formatSessionDigest(antecedents, r.location)
	}

	result, err := r.reconcileWithModel(ctx, DossierReconcileRequest{
		ContactName:      contactName,
		PriorMarkdown:    priorMarkdown(prior),
		PriorState:       priorState(prior),
		SessionDigest:    formatSessionDigest(sessions, r.location),
		AntecedentDigest: antecedentDigest,
		AnchorDate:       anchor,
	})
	if err != nil {
		return nil, err
	}

	state := result.StructuredState
	if state == nil {
		state = make(map[string]any)
	}

	// Deterministic reconciliation, independent of the model.
	AnchorRelativeDates(state, anchor)
	ApplyChronologicalOverrides(state, result.Supersedes)
	TransitionPastEvents(state, anchor)

	state = r.enrichWithContacts(ctx, state, participants, contactName)

	markdown := RenderDossierMarkdown(contactName, state, result.Markdown)

	triggerSessionID := latest.ID
	lastProcessed := latest.EndedAt.UTC()

	saved, err := r.store.UpsertContactDossier(
		ctx, conversationID, contactName, markdown, state, &triggerSessionID, &lastProcessed,
	)
	if err != nil {
		return nil, fmt.Errorf("persist reconciled dossier: %w", err)
	}

	path, writeErr := r.writeMarkdown(contactName, conversationID, markdown)
	if writeErr != nil {
		r.logger.Warn().Err(writeErr).Str("conversation_id", conversationID).Msg("Failed to export dossier Markdown")
	}

	r.logger.Info().
		Str("conversation_id", conversationID).
		Int("session_count", len(sessions)).
		Int("version", saved.Version).
		Str("markdown_path", path).
		Msg("Reconciled Living Dossier")

	return &DossierOutcome{Dossier: saved, MarkdownPath: path, Changed: true}, nil
}

// loadPriorDossier returns the existing dossier, or nil when the contact has none yet.
func (r *DossierReconciler) loadPriorDossier(ctx context.Context, conversationID string) (*db.ContactDossier, error) {
	prior, err := r.store.GetContactDossier(ctx, conversationID)
	switch {
	case err == nil:
		return prior, nil
	case errors.Is(err, db.ErrNotFound):
		return nil, nil
	default:
		return nil, fmt.Errorf("load prior dossier: %w", err)
	}
}

// loadConversationContext returns the conversation display name and participant identifiers.
func (r *DossierReconciler) loadConversationContext(ctx context.Context, conversationID string) (string, []string, error) {
	conv, err := r.store.GetConversation(ctx, conversationID)
	switch {
	case err == nil && conv != nil:
		return conv.Name, conv.Participants, nil
	case errors.Is(err, db.ErrNotFound):
		return "", nil, nil
	case err != nil:
		return "", nil, fmt.Errorf("load conversation: %w", err)
	default:
		return "", nil, nil
	}
}

// reconcileWithModel invokes the injected model, degrading gracefully when none is configured.
func (r *DossierReconciler) reconcileWithModel(ctx context.Context, req DossierReconcileRequest) (*DossierReconcileResult, error) {
	if r.model == nil {
		return &DossierReconcileResult{StructuredState: map[string]any{}}, nil
	}
	result, err := r.model.Reconcile(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("dossier model reconciliation: %w", err)
	}
	if result == nil {
		result = &DossierReconcileResult{StructuredState: map[string]any{}}
	}
	return result, nil
}

// enrichWithContacts merges Google Contacts ground truth into the structured state (PIPE-04).
func (r *DossierReconciler) enrichWithContacts(ctx context.Context, state map[string]any, participants []string, contactName string) map[string]any {
	if r.contactSource == nil {
		return state
	}
	contacts, err := r.contactSource(ctx)
	if err != nil {
		r.logger.Warn().Err(err).Msg("Contacts enrichment failed; continuing without contact metadata")
		return state
	}
	return EnrichStructuredStateWithContacts(state, contacts, participants, contactName)
}

// writeMarkdown exports the rendered dossier to <outputDir>/<slug>.md and returns the path.
func (r *DossierReconciler) writeMarkdown(contactName, conversationID, markdown string) (string, error) {
	if strings.TrimSpace(r.outputDir) == "" {
		return "", nil
	}
	if err := os.MkdirAll(r.outputDir, 0o755); err != nil {
		return "", fmt.Errorf("create dossier output dir: %w", err)
	}

	slug := slugify(contactName)
	if slug == "" {
		slug = slugify(conversationID)
	}
	if slug == "" {
		slug = "dossier"
	}

	path := filepath.Join(r.outputDir, slug+".md")
	if err := os.WriteFile(path, []byte(markdown), 0o644); err != nil {
		return "", fmt.Errorf("write dossier markdown: %w", err)
	}
	return path, nil
}

// slugify converts a name into a lowercase, hyphen-delimited file slug.
func slugify(name string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// formatSessionDigest renders sessions with absolute calendar bounds for the model prompt.
func formatSessionDigest(sessions []db.ConversationSession, loc *time.Location) string {
	if loc == nil {
		loc = time.UTC
	}

	var b strings.Builder
	for _, sess := range sessions {
		start := sess.StartedAt.In(loc).Format("2006-01-02 15:04")
		end := sess.EndedAt.In(loc).Format("2006-01-02 15:04")
		fmt.Fprintf(&b, "=== Session %d [%s – %s] (%d messages) ===\n", sess.ID, start, end, sess.MessageCount)
		if sess.Summary != nil && strings.TrimSpace(*sess.Summary) != "" {
			fmt.Fprintf(&b, "Summary: %s\n", strings.TrimSpace(*sess.Summary))
		}
		if strings.TrimSpace(sess.Transcript) != "" {
			fmt.Fprintf(&b, "Transcript:\n%s\n", strings.TrimSpace(sess.Transcript))
		}
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

// priorMarkdown returns the prior dossier Markdown stripped of rendered event sections, or an empty string.
func priorMarkdown(prior *db.ContactDossier) string {
	if prior == nil {
		return ""
	}
	return StripRenderedDossierSections(prior.MarkdownContent)
}

// priorState returns the prior dossier structured state, or an empty map.
func priorState(prior *db.ContactDossier) map[string]any {
	if prior == nil || prior.StructuredState == nil {
		return map[string]any{}
	}
	return prior.StructuredState
}
