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
