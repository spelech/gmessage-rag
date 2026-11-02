# Hybrid RAG & Vector Search (`DB-03`, `DB-04`)

`gmessages-rag` uses a hybrid retrieval strategy combining lexical full-text search with dense vector similarity.

## 2-Hour Session Windowing (`DB-03`)

Contiguous messages separated by $\le 2\text{ hours}$ of silence are grouped into a single `conversation_session`:
- Maintains conversational context across back-and-forth threads.
- Formats transcripts with absolute calendar anchors (`[YYYY-MM-DD HH:MM:SS UTC]`).
- Appends vision OCR text and image descriptions directly inline.

## Reciprocal Rank Fusion (RRF) Formulation (`DB-04`)

To combine full-text BM25 rankings with vector cosine distances, the query uses Reciprocal Rank Fusion ($k = 60$):

$$RRF(d) = \frac{1}{k + r_{\text{text}}(d)} + \frac{1}{k + r_{\text{vector}}(d)}$$

Where:
- $r_{\text{text}}(d)$ is the dense rank from `ts_rank_cd(search_tsv, query)`.
- $r_{\text{vector}}(d)$ is the dense rank from cosine distance `embedding <=> query_vector`.
- $k$ is the smoothing constant (default $60$, adjustable via the runtime tuner).

## PostgreSQL Query Construction

```sql
WITH text_matches AS (
    SELECT id, RANK() OVER (ORDER BY ts_rank_cd(search_tsv, websearch_to_tsquery('english', $1)) DESC) AS text_rank
    FROM conversation_sessions
    WHERE search_tsv @@ websearch_to_tsquery('english', $1)
    LIMIT 50
),
vector_matches AS (
    SELECT id, RANK() OVER (ORDER BY embedding <=> $2::halfvec) AS vector_rank
    FROM conversation_sessions
    WHERE is_processed = true
    LIMIT 50
)
SELECT s.id, s.conversation_id, s.started_at, s.ended_at, s.summary, s.transcript,
       COALESCE(1.0 / ($3 + tm.text_rank), 0.0) + COALESCE(1.0 / ($3 + vm.vector_rank), 0.0) AS rrf_score
FROM conversation_sessions s
LEFT JOIN text_matches tm ON s.id = tm.id
LEFT JOIN vector_matches vm ON s.id = vm.id
WHERE tm.id IS NOT NULL OR vm.id IS NOT NULL
ORDER BY rrf_score DESC
LIMIT $4;
```
