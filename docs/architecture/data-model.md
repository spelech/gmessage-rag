# Data Model & Schema Architecture

The persistent layer uses PostgreSQL 17 with the `pgvector` extension, leveraging `halfvec(1536)` embeddings and full-text search dictionaries.

## Entity Relationship Diagram

```mermaid
erDiagram
    conversations ||--o{ messages : "contains"
    conversations ||--o{ conversation_sessions : "grouped into"
    conversations ||--o{ media_attachments : "receives"
    conversations ||--o| contact_dossiers : "has current state"
    conversations ||--o{ contact_dossier_history : "records audits"
    conversation_sessions ||--o{ messages : "indexes"
    messages ||--o{ media_attachments : "attaches"

    conversations {
        text id PK
        text name
        jsonb participants
        boolean is_group
        boolean is_tracked
        timestamptz last_message_at
        text backfill_cursor
        boolean backfill_complete
        jsonb metadata
    }

    messages {
        text id PK
        text conversation_id FK
        bigint session_id FK
        text sender_id
        text sender_name
        boolean is_from_me
        timestamptz sent_at
        text body
        text reply_to_message_id
        jsonb reactions
        int raw_status
        boolean is_backfill
    }

    conversation_sessions {
        bigserial id PK
        text conversation_id FK
        timestamptz started_at
        timestamptz ended_at
        int message_count
        text summary
        text transcript
        tsvector search_tsv
        halfvec embedding
        boolean is_processed
    }

    media_attachments {
        bigserial id PK
        text message_id FK
        text conversation_id FK
        text sha256_hash
        text relative_path
        text mime_type
        bigint file_size_bytes
        int width
        int height
        text ocr_text
        text visual_description
        boolean vision_processed
    }

    contact_dossiers {
        text conversation_id PK,FK
        text contact_name
        text markdown_content
        jsonb structured_state
        timestamptz last_processed_message_at
        int version
        timestamptz updated_at
    }

    contact_dossier_history {
        bigserial id PK
        text conversation_id FK
        int version
        text markdown_content
        jsonb structured_state
        bigint trigger_session_id FK
        timestamptz created_at
    }
```

## Schema Highlights

- **HNSW halfvec Indexing**: Embeddings are stored as `halfvec(1536)` with an HNSW cosine index (`vector_cosine_ops`), cutting index memory consumption by 50% while maintaining high recall.
- **Full-Text GIN Indexing**: `conversation_sessions.search_tsv` maintains an indexed lexeme representation generated via `to_tsvector('english', transcript)`.
- **Dossier Audit History**: Every update to `contact_dossiers` creates an immutable row in `contact_dossier_history` with the triggering session ID and timestamp.
