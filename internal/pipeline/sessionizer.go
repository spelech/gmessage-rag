package pipeline

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pgvector/pgvector-go"
	"github.com/rs/zerolog"
	"github.com/spelech/gmessage-rag/internal/db"
)

// SessionStore defines the database methods needed by the Sessionizer.
type SessionStore interface {
	GetUnassignedMessages(ctx context.Context, conversationID string) ([]db.Message, error)
	UpsertSession(ctx context.Context, session *db.ConversationSession, messageIDs []string) (*db.ConversationSession, error)
	ListSessionsNeedingAnalysis(ctx context.Context, conversationID string, limit int) ([]db.ConversationSession, error)
	GetSessionMessages(ctx context.Context, sessionID int64) ([]db.Message, error)
	UpdateSessionAnalysis(ctx context.Context, sessionID int64, summary string, embedding *pgvector.HalfVector) error
}

// SessionizerConfig configures the Sessionizer.
type SessionizerConfig struct {
	Store           SessionStore
	LLMClient       LLMClient
	SessionGapHours int
	Location        *time.Location
	Logger          zerolog.Logger
	// Workers is the number of sessions re-summarized concurrently. Zero or
	// negative defaults to 4.
	Workers int
}

// Sessionizer chunks raw conversation messages into coherent 2-hour contiguous sessions (PIPE-06 / DB-03),
// summarizes them using an LLM, embeds them into 1536-dim halfvec embeddings, and persists them via SessionStore.
type Sessionizer struct {
	store      SessionStore
	llmClient  LLMClient
	sessionGap time.Duration
	location   *time.Location
	logger     zerolog.Logger
	workers    int
}

// NewSessionizer initializes a new Sessionizer.
func NewSessionizer(cfg SessionizerConfig) *Sessionizer {
	gapHours := cfg.SessionGapHours
	if gapHours <= 0 {
		gapHours = 2
	}
	loc := cfg.Location
	if loc == nil {
		loc = time.UTC
	}
	workers := cfg.Workers
	if workers <= 0 {
		workers = 4
	}

	return &Sessionizer{
		store:      cfg.Store,
		llmClient:  cfg.LLMClient,
		sessionGap: time.Duration(gapHours) * time.Hour,
		location:   loc,
		logger:     cfg.Logger,
		workers:    workers,
	}
}

// ChunkMessages partitions an arbitrary slice of messages into contiguous groups
// where the interval between successive messages does not exceed sessionGap.
func (s *Sessionizer) ChunkMessages(msgs []db.Message) [][]db.Message {
	if len(msgs) == 0 {
		return nil
	}

	// Work on a sorted shallow copy
	sortedMsgs := make([]db.Message, len(msgs))
	copy(sortedMsgs, msgs)
	sort.SliceStable(sortedMsgs, func(i, j int) bool {
		return sortedMsgs[i].SentAt.Before(sortedMsgs[j].SentAt)
	})

	var chunks [][]db.Message
	currentChunk := []db.Message{sortedMsgs[0]}

	for i := 1; i < len(sortedMsgs); i++ {
		prevTime := sortedMsgs[i-1].SentAt
		currTime := sortedMsgs[i].SentAt

		if currTime.Sub(prevTime) > s.sessionGap {
			chunks = append(chunks, currentChunk)
			currentChunk = []db.Message{sortedMsgs[i]}
		} else {
			currentChunk = append(currentChunk, sortedMsgs[i])
		}
	}

	if len(currentChunk) > 0 {
		chunks = append(chunks, currentChunk)
	}

	return chunks
}

// FormatTranscript converts a slice of messages into a chronological formatted transcript:
// [YYYY-MM-DD HH:MM:SS] Sender: Message text
func (s *Sessionizer) FormatTranscript(msgs []db.Message) string {
	if len(msgs) == 0 {
		return ""
	}

	var sb strings.Builder
	for i, m := range msgs {
		t := m.SentAt.In(s.location)
		sender := m.SenderName
		if sender == "" {
			if m.IsFromMe {
				sender = "Me"
			} else {
				sender = m.SenderID
			}
		}
		body := m.Body
		if len(m.Reactions) > 0 {
			var rStrs []string
			for _, r := range m.Reactions {
				rStrs = append(rStrs, r.Emoji)
			}
			body += fmt.Sprintf(" [%s]", strings.Join(rStrs, " "))
		}

		fmt.Fprintf(&sb, "[%s] %s: %s", t.Format("2006-01-02 15:04:05"), sender, body)
		if i < len(msgs)-1 {
			sb.WriteString("\n")
		}
	}

	return sb.String()
}

// ProcessSession processes a single contiguous chunk of messages:
// formats transcript, requests LLM title & summary, generates dense vector embedding,
// and persists via db.UpsertSession.
func (s *Sessionizer) ProcessSession(ctx context.Context, conversationID string, chunk []db.Message) (*db.ConversationSession, error) {
	if s.store == nil {
		return nil, errors.New("store cannot be nil")
	}
	if len(chunk) == 0 {
		return nil, errors.New("cannot process empty message chunk")
	}
	if conversationID == "" {
		return nil, errors.New("conversationID cannot be empty")
	}

	transcript := s.FormatTranscript(chunk)
	startedAt := chunk[0].SentAt.UTC()
	endedAt := chunk[len(chunk)-1].SentAt.UTC()

	var summaryStr *string
	var hv *pgvector.HalfVector

	if s.llmClient != nil {
		title, sumText, err := s.llmClient.GenerateSummary(ctx, transcript)
		if err != nil {
			s.logger.Warn().Err(err).Str("conv_id", conversationID).Msg("LLM summary generation failed; continuing without summary")
		} else {
			combined := fmt.Sprintf("%s: %s", title, sumText)
			summaryStr = &combined
		}

		embedInput := transcript
		if summaryStr != nil && *summaryStr != "" {
			embedInput = *summaryStr + "\n\n" + transcript
		}

		vec, err := s.llmClient.GenerateEmbedding(ctx, embedInput)
		if err != nil {
			s.logger.Warn().Err(err).Str("conv_id", conversationID).Msg("Embedding generation failed; continuing without embedding")
		} else if len(vec) > 0 {
			v := pgvector.NewHalfVector(vec)
			hv = &v
		}
	}

	messageIDs := make([]string, len(chunk))
	for i, m := range chunk {
		messageIDs[i] = m.ID
	}

	sess := &db.ConversationSession{
		ConversationID: conversationID,
		StartedAt:      startedAt,
		EndedAt:        endedAt,
		MessageCount:   len(chunk),
		Summary:        summaryStr,
		Transcript:     transcript,
		Embedding:      hv,
		IsProcessed:    true,
	}

	saved, err := s.store.UpsertSession(ctx, sess, messageIDs)
	if err != nil {
		return nil, fmt.Errorf("upsert session: %w", err)
	}

	return saved, nil
}

// ProcessUnassignedMessages loads unassigned messages for conversationID, chunks them,
// and processes each chunk with bounded memory.
func (s *Sessionizer) ProcessUnassignedMessages(ctx context.Context, conversationID string) ([]*db.ConversationSession, error) {
	if s.store == nil {
		return nil, errors.New("store cannot be nil")
	}
	if conversationID == "" {
		return nil, errors.New("conversationID cannot be empty")
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	unassigned, err := s.store.GetUnassignedMessages(ctx, conversationID)
	if err != nil {
		return nil, fmt.Errorf("get unassigned messages: %w", err)
	}

	if len(unassigned) == 0 {
		return nil, nil
	}

	chunks := s.ChunkMessages(unassigned)
	sessions := make([]*db.ConversationSession, 0, len(chunks))

	for _, chunk := range chunks {
		select {
		case <-ctx.Done():
			return sessions, ctx.Err()
		default:
		}

		sess, err := s.ProcessSession(ctx, conversationID, chunk)
		if err != nil {
			return sessions, fmt.Errorf("process session chunk: %w", err)
		}
		sessions = append(sessions, sess)
	}

	return sessions, nil
}

// ResummarizeReport summarizes a re-summarization pass.
type ResummarizeReport struct {
	Requested int      `json:"requested"`
	Updated   int      `json:"updated"`
	Skipped   int      `json:"skipped"`
	Failed    int      `json:"failed"`
	Errors    []string `json:"errors,omitempty"`
}

// resummarizeOutcome classifies the result of one session attempt.
type resummarizeOutcome int

const (
	outcomeUpdated resummarizeOutcome = iota
	outcomeSkipped
	outcomeFailed
)

// resummarizeOne regenerates one session's summary and embedding in place.
func (s *Sessionizer) resummarizeOne(ctx context.Context, sess db.ConversationSession) (resummarizeOutcome, error) {
	msgs, err := s.store.GetSessionMessages(ctx, sess.ID)
	if err != nil {
		return outcomeFailed, fmt.Errorf("load messages: %w", err)
	}
	if len(msgs) == 0 {
		return outcomeSkipped, nil
	}

	transcript := s.FormatTranscript(msgs)
	title, sumText, err := s.llmClient.GenerateSummary(ctx, transcript)
	if err != nil {
		return outcomeFailed, fmt.Errorf("summary: %w", err)
	}

	combined := fmt.Sprintf("%s: %s", title, sumText)
	var hv *pgvector.HalfVector
	if vec, embedErr := s.llmClient.GenerateEmbedding(ctx, combined+"\n\n"+transcript); embedErr != nil {
		s.logger.Warn().Err(embedErr).Int64("session_id", sess.ID).Msg("Embedding generation failed during resummarize; storing summary only")
	} else if len(vec) > 0 {
		v := pgvector.NewHalfVector(vec)
		hv = &v
	}

	if err := s.store.UpdateSessionAnalysis(ctx, sess.ID, combined, hv); err != nil {
		return outcomeFailed, fmt.Errorf("persist: %w", err)
	}
	return outcomeUpdated, nil
}

// ResummarizeMissing regenerates summaries and embeddings for existing sessions
// that lack them. It keeps each session's original boundaries and message
// linkage; only the summary and embedding are replaced. When conversationID is
// empty, all conversations are processed. A limit of zero defaults to 1000.
// Sessions are processed by a bounded worker pool (SessionizerConfig.Workers).
func (s *Sessionizer) ResummarizeMissing(ctx context.Context, conversationID string, limit int) (*ResummarizeReport, error) {
	if s.store == nil {
		return nil, errors.New("store cannot be nil")
	}
	if s.llmClient == nil {
		return nil, errors.New("llm client cannot be nil")
	}

	sessions, err := s.store.ListSessionsNeedingAnalysis(ctx, conversationID, limit)
	if err != nil {
		return nil, fmt.Errorf("list sessions needing analysis: %w", err)
	}

	report := &ResummarizeReport{Requested: len(sessions)}
	if len(sessions) == 0 {
		return report, nil
	}

	workers := s.workers
	if workers < 1 {
		workers = 1
	}
	if workers > len(sessions) {
		workers = len(sessions)
	}

	var (
		mu sync.Mutex
		wg sync.WaitGroup
	)
	jobs := make(chan db.ConversationSession)
	worker := func() {
		defer wg.Done()
		for sess := range jobs {
			select {
			case <-ctx.Done():
				return
			default:
			}
			outcome, runErr := s.resummarizeOne(ctx, sess)
			mu.Lock()
			switch outcome {
			case outcomeUpdated:
				report.Updated++
			case outcomeSkipped:
				report.Skipped++
			case outcomeFailed:
				report.Failed++
				if runErr != nil {
					report.Errors = append(report.Errors, fmt.Sprintf("session %d: %v", sess.ID, runErr))
				}
			}
			mu.Unlock()
		}
	}

	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go worker()
	}

dispatch:
	for _, sess := range sessions {
		select {
		case <-ctx.Done():
			break dispatch
		case jobs <- sess:
		}
	}
	close(jobs)
	wg.Wait()

	if err := ctx.Err(); err != nil {
		return report, err
	}
	return report, nil
}
