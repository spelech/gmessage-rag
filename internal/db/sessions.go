package db

import (
	"context"
	"fmt"
	"time"

	"github.com/pgvector/pgvector-go"
)

// ConversationSession represents a contiguous 2-hour conversation session record.
type ConversationSession struct {
	ID             int64                `json:"id"`
	ConversationID string               `json:"conversation_id"`
	StartedAt      time.Time            `json:"started_at"`
	EndedAt        time.Time            `json:"ended_at"`
	MessageCount   int                  `json:"message_count"`
	Summary        *string              `json:"summary,omitempty"`
	Transcript     string               `json:"transcript"`
	Embedding      *pgvector.HalfVector `json:"embedding,omitempty"`
	IsProcessed    bool                 `json:"is_processed"`
	CreatedAt      time.Time            `json:"created_at"`
	UpdatedAt      time.Time            `json:"updated_at"`
}

// SessionSearchResult represents a ranked session returned by hybrid search.
type SessionSearchResult struct {
	ID             int64     `json:"id"`
	ConversationID string    `json:"conversation_id"`
	StartedAt      time.Time `json:"started_at"`
	EndedAt        time.Time `json:"ended_at"`
	Summary        *string   `json:"summary,omitempty"`
	Transcript     string    `json:"transcript"`
	RRFScore       float64   `json:"rrf_score"`
}

// UpsertSession inserts or updates a conversation session and links the specified message IDs.
func (d *DB) UpsertSession(ctx context.Context, session *ConversationSession, messageIDs []string) (*ConversationSession, error) {
	tx, err := d.Pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	const upsertSessionSQL = `
INSERT INTO conversation_sessions (
    conversation_id,
    started_at,
    ended_at,
    message_count,
    summary,
    transcript,
    embedding,
    is_processed,
    updated_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NOW())
ON CONFLICT (conversation_id, started_at) DO UPDATE SET
    ended_at = EXCLUDED.ended_at,
    message_count = EXCLUDED.message_count,
    summary = EXCLUDED.summary,
    transcript = EXCLUDED.transcript,
    embedding = EXCLUDED.embedding,
    is_processed = EXCLUDED.is_processed,
    updated_at = NOW()
RETURNING id, created_at, updated_at;
`

	var embeddingArg any
	if session.Embedding != nil {
		embeddingArg = session.Embedding.String()
	}

	err = tx.QueryRow(ctx, upsertSessionSQL,
		session.ConversationID,
		session.StartedAt,
		session.EndedAt,
		session.MessageCount,
		session.Summary,
		session.Transcript,
		embeddingArg,
		session.IsProcessed,
	).Scan(&session.ID, &session.CreatedAt, &session.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("upsert conversation_sessions: %w", err)
	}

	if len(messageIDs) > 0 {
		const linkMessagesSQL = `
UPDATE messages
SET session_id = $1
WHERE conversation_id = $2 AND id = ANY($3);
`
		_, err = tx.Exec(ctx, linkMessagesSQL, session.ID, session.ConversationID, messageIDs)
		if err != nil {
			return nil, fmt.Errorf("link messages to session: %w", err)
		}

		// Insert causal message_links for any explicit replies between sessions
		const linkRepliesSQL = `
INSERT INTO message_links (
    conversation_id,
    source_session_id,
    target_session_id,
    source_message_id,
    target_message_id,
    link_type,
    confidence,
    evidence,
    status,
    created_by
)
SELECT
    m1.conversation_id,
    m1.session_id,
    m2.session_id,
    m1.id,
    m2.id,
    'explicit_reply',
    1.000,
    jsonb_build_object('source_sent_at', m1.sent_at, 'target_sent_at', m2.sent_at),
    'detected',
    'detector:protocol_reply'
FROM messages m1
JOIN messages m2 ON m1.reply_to_message_id = m2.id
WHERE m1.id = ANY($1)
  AND m1.session_id IS NOT NULL
  AND m2.session_id IS NOT NULL
  AND m1.session_id <> m2.session_id
ON CONFLICT DO NOTHING;
`
		_, _ = tx.Exec(ctx, linkRepliesSQL, messageIDs)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit tx: %w", err)
	}

	return session, nil
}

// SearchSessionsHybrid executes the Hybrid Reciprocal Rank Fusion (RRF) SQL query combining full-text search and vector cosine similarity.
func (d *DB) SearchSessionsHybrid(
	ctx context.Context,
	conversationID string,
	queryText string,
	queryVec []float32,
	dateFrom *time.Time,
	dateTo *time.Time,
	limit int,
) ([]SessionSearchResult, error) {
	const rrfQuery = `
WITH fts_matches AS (
    SELECT id, ROW_NUMBER() OVER (ORDER BY ts_rank_cd(search_tsv, websearch_to_tsquery('english', $2)) DESC) AS rank_ix
    FROM conversation_sessions
    WHERE conversation_id = $1
      AND ($3::timestamptz IS NULL OR started_at >= $3)
      AND ($4::timestamptz IS NULL OR ended_at <= $4)
      AND search_tsv @@ websearch_to_tsquery('english', $2)
    LIMIT 40
),
vec_matches AS (
    SELECT id, ROW_NUMBER() OVER (ORDER BY embedding <=> $5::halfvec) AS rank_ix
    FROM conversation_sessions
    WHERE conversation_id = $1
      AND ($3::timestamptz IS NULL OR started_at >= $3)
      AND ($4::timestamptz IS NULL OR ended_at <= $4)
      AND embedding IS NOT NULL
    LIMIT 40
),
rrf AS (
    SELECT COALESCE(f.id, v.id) AS id,
           COALESCE(1.0 / (60 + f.rank_ix), 0.0) + COALESCE(1.0 / (60 + v.rank_ix), 0.0) AS rrf_score
    FROM fts_matches f FULL OUTER JOIN vec_matches v ON f.id = v.id
)
SELECT s.id, s.conversation_id, s.started_at, s.ended_at, s.summary, s.transcript, r.rrf_score
FROM rrf r JOIN conversation_sessions s ON s.id = r.id
ORDER BY r.rrf_score DESC LIMIT $6;
`

	var vecArg any
	if queryVec != nil {
		hv := pgvector.NewHalfVector(queryVec)
		vecArg = hv.String()
	}

	rows, err := d.Pool.Query(ctx, rrfQuery, conversationID, queryText, dateFrom, dateTo, vecArg, limit)
	if err != nil {
		return nil, fmt.Errorf("execute hybrid rrf query: %w", err)
	}
	defer rows.Close()

	var results []SessionSearchResult
	for rows.Next() {
		var res SessionSearchResult
		if err := rows.Scan(
			&res.ID,
			&res.ConversationID,
			&res.StartedAt,
			&res.EndedAt,
			&res.Summary,
			&res.Transcript,
			&res.RRFScore,
		); err != nil {
			return nil, fmt.Errorf("scan hybrid search row: %w", err)
		}
		results = append(results, res)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate hybrid search rows: %w", err)
	}

	return results, nil
}

// SearchSessionsHybrid_RRF executes hybrid reciprocal rank fusion search with default nil vector and dates.
func (d *DB) SearchSessionsHybrid_RRF(
	ctx context.Context,
	conversationID string,
	queryText string,
	limit int,
) ([]SessionSearchResult, error) {
	return d.SearchSessionsHybrid(ctx, conversationID, queryText, nil, nil, nil, limit)
}

// SearchSessionsHybridAllConversations executes Hybrid RRF fusion across every conversation.
func (d *DB) SearchSessionsHybridAllConversations(
	ctx context.Context,
	queryText string,
	queryVec []float32,
	dateFrom *time.Time,
	dateTo *time.Time,
	limit int,
) ([]SessionSearchResult, error) {
	const rrfQueryAll = `
WITH fts_matches AS (
    SELECT id, ROW_NUMBER() OVER (ORDER BY ts_rank_cd(search_tsv, websearch_to_tsquery('english', $1)) DESC) AS rank_ix
    FROM conversation_sessions
    WHERE ($2::timestamptz IS NULL OR started_at >= $2)
      AND ($3::timestamptz IS NULL OR ended_at <= $3)
      AND search_tsv @@ websearch_to_tsquery('english', $1)
    LIMIT 40
),
vec_matches AS (
    SELECT id, ROW_NUMBER() OVER (ORDER BY embedding <=> $4::halfvec) AS rank_ix
    FROM conversation_sessions
    WHERE ($2::timestamptz IS NULL OR started_at >= $2)
      AND ($3::timestamptz IS NULL OR ended_at <= $3)
      AND embedding IS NOT NULL
    LIMIT 40
),
rrf AS (
    SELECT COALESCE(f.id, v.id) AS id,
           COALESCE(1.0 / (60 + f.rank_ix), 0.0) + COALESCE(1.0 / (60 + v.rank_ix), 0.0) AS rrf_score
    FROM fts_matches f FULL OUTER JOIN vec_matches v ON f.id = v.id
)
SELECT s.id, s.conversation_id, s.started_at, s.ended_at, s.summary, s.transcript, r.rrf_score
FROM rrf r JOIN conversation_sessions s ON s.id = r.id
ORDER BY r.rrf_score DESC LIMIT $5;
`

	var vecArg any
	if queryVec != nil {
		hv := pgvector.NewHalfVector(queryVec)
		vecArg = hv.String()
	}

	rows, err := d.Pool.Query(ctx, rrfQueryAll, queryText, dateFrom, dateTo, vecArg, limit)
	if err != nil {
		return nil, fmt.Errorf("execute hybrid rrf query (all conversations): %w", err)
	}
	defer rows.Close()

	var results []SessionSearchResult
	for rows.Next() {
		var res SessionSearchResult
		if err := rows.Scan(
			&res.ID,
			&res.ConversationID,
			&res.StartedAt,
			&res.EndedAt,
			&res.Summary,
			&res.Transcript,
			&res.RRFScore,
		); err != nil {
			return nil, fmt.Errorf("scan hybrid search row: %w", err)
		}
		results = append(results, res)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate hybrid search rows: %w", err)
	}

	return results, nil
}

// GetSessionsSince returns conversation sessions that end after since (ordered chronologically ASC).
// When since is nil, every session for the conversation is returned. It backs the Living Dossier
// reconciler (PIPE-03), which folds only sessions newer than the dossier's last processed message.
func (d *DB) GetSessionsSince(ctx context.Context, conversationID string, since *time.Time) ([]ConversationSession, error) {
	if conversationID == "" {
		return nil, fmt.Errorf("conversation ID cannot be empty")
	}

	const getSessionsSinceSQL = `
SELECT
    id, conversation_id, started_at, ended_at, message_count,
    summary, transcript, is_processed, created_at, updated_at
FROM conversation_sessions
WHERE conversation_id = $1
  AND ($2::timestamptz IS NULL OR ended_at > $2)
ORDER BY started_at ASC;
`

	rows, err := d.Pool.Query(ctx, getSessionsSinceSQL, conversationID, since)
	if err != nil {
		return nil, fmt.Errorf("query conversation sessions since: %w", err)
	}
	defer rows.Close()

	var sessions []ConversationSession
	for rows.Next() {
		var sess ConversationSession
		if err := rows.Scan(
			&sess.ID,
			&sess.ConversationID,
			&sess.StartedAt,
			&sess.EndedAt,
			&sess.MessageCount,
			&sess.Summary,
			&sess.Transcript,
			&sess.IsProcessed,
			&sess.CreatedAt,
			&sess.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan session row: %w", err)
		}
		sessions = append(sessions, sess)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate session rows: %w", err)
	}

	return sessions, nil
}

// ListSessionsNeedingAnalysis returns sessions missing a summary or an embedding.
// When conversationID is empty, all conversations are considered.
func (d *DB) ListSessionsNeedingAnalysis(ctx context.Context, conversationID string, limit int) ([]ConversationSession, error) {
	if limit <= 0 {
		limit = 1000
	}
	const q = `
SELECT id, conversation_id, started_at, ended_at, message_count
FROM conversation_sessions
WHERE (summary IS NULL OR summary = '' OR embedding IS NULL)
  AND message_count > 0
  AND ($1 = '' OR conversation_id = $1)
ORDER BY conversation_id ASC, started_at ASC
LIMIT $2;
`
	rows, err := d.Pool.Query(ctx, q, conversationID, limit)
	if err != nil {
		return nil, fmt.Errorf("query sessions needing analysis: %w", err)
	}
	defer rows.Close()

	var sessions []ConversationSession
	for rows.Next() {
		var s ConversationSession
		if err := rows.Scan(&s.ID, &s.ConversationID, &s.StartedAt, &s.EndedAt, &s.MessageCount); err != nil {
			return nil, fmt.Errorf("scan session needing analysis: %w", err)
		}
		sessions = append(sessions, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sessions needing analysis: %w", err)
	}
	return sessions, nil
}

// UpdateSessionAnalysis stores a regenerated summary and embedding for an
// existing session, without changing its boundaries or message linkage.
func (d *DB) UpdateSessionAnalysis(ctx context.Context, sessionID int64, summary string, embedding *pgvector.HalfVector) error {
	if sessionID <= 0 {
		return fmt.Errorf("session ID must be positive")
	}
	var embeddingArg any
	if embedding != nil {
		embeddingArg = embedding.String()
	}
	const q = `
UPDATE conversation_sessions
SET summary = $2, embedding = $3, is_processed = TRUE, updated_at = NOW()
WHERE id = $1;
`
	tag, err := d.Pool.Exec(ctx, q, sessionID, summary, embeddingArg)
	if err != nil {
		return fmt.Errorf("update session analysis: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
