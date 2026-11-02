package pipeline

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/pgvector/pgvector-go"
	"github.com/rs/zerolog"
	"github.com/spelech/gmessage-rag/internal/db"
)

// SessionStore defines the database methods needed by the Sessionizer.
type SessionStore interface {
	GetUnassignedMessages(ctx context.Context, conversationID string) ([]db.Message, error)
	UpsertSession(ctx context.Context, session *db.ConversationSession, messageIDs []string) (*db.ConversationSession, error)
}

// SessionizerConfig configures the Sessionizer.
type SessionizerConfig struct {
	Store           SessionStore
	LLMClient       LLMClient
	SessionGapHours int
	Location        *time.Location
	Logger          zerolog.Logger
}

// Sessionizer chunks raw conversation messages into coherent 2-hour contiguous sessions (PIPE-06 / DB-03),
// summarizes them using an LLM, embeds them into 1536-dim halfvec embeddings, and persists them via SessionStore.
type Sessionizer struct {
	store      SessionStore
	llmClient  LLMClient
	sessionGap time.Duration
	location   *time.Location
	logger     zerolog.Logger
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

	return &Sessionizer{
		store:      cfg.Store,
		llmClient:  cfg.LLMClient,
		sessionGap: time.Duration(gapHours) * time.Hour,
		location:   loc,
		logger:     cfg.Logger,
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
