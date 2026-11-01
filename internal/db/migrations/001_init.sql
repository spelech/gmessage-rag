CREATE EXTENSION IF NOT EXISTS vector;
CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- 1. Conversations / Threads
CREATE TABLE IF NOT EXISTS conversations (
    id TEXT PRIMARY KEY,                        -- libgm ConversationID
    name TEXT NOT NULL DEFAULT '',              -- Contact display name or group title
    participants JSONB NOT NULL DEFAULT '[]',   -- Array of phone numbers / identifiers
    is_group BOOLEAN NOT NULL DEFAULT FALSE,
    is_tracked BOOLEAN NOT NULL DEFAULT FALSE,  -- If true, runs Vision OCR + Living Dossier
    last_message_at TIMESTAMPTZ,
    backfill_cursor TEXT,                       -- Pagination cursor for historical fetch
    backfill_complete BOOLEAN NOT NULL DEFAULT FALSE,
    metadata JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 2. Conversation Sessions (Primary RAG Unit: 2-hour contiguous windows)
CREATE TABLE IF NOT EXISTS conversation_sessions (
    id BIGSERIAL PRIMARY KEY,
    conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    started_at TIMESTAMPTZ NOT NULL,
    ended_at TIMESTAMPTZ NOT NULL,
    message_count INT NOT NULL DEFAULT 0,
    summary TEXT,                               -- Concise LLM summary of the session arc
    transcript TEXT NOT NULL,                   -- Formatted transcript w/ explicit dates & OCR
    search_tsv TSVECTOR GENERATED ALWAYS AS (
        setweight(to_tsvector('english', coalesce(summary, '')), 'A') ||
        setweight(to_tsvector('english', coalesce(transcript, '')), 'B')
    ) STORED,
    embedding halfvec(1536),                    -- Configurable dimension via env
    is_processed BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (conversation_id, started_at)
);

CREATE INDEX IF NOT EXISTS idx_sessions_conv_time ON conversation_sessions (conversation_id, started_at DESC);
CREATE INDEX IF NOT EXISTS idx_sessions_tsv ON conversation_sessions USING GIN (search_tsv);
CREATE INDEX IF NOT EXISTS idx_sessions_vec ON conversation_sessions USING hnsw (embedding halfvec_cosine_ops);

-- 3. Individual Raw Messages
CREATE TABLE IF NOT EXISTS messages (
    id TEXT PRIMARY KEY,                        -- libgm MessageID
    conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    session_id BIGINT REFERENCES conversation_sessions(id) ON DELETE SET NULL,
    sender_id TEXT NOT NULL,                    -- Participant ID or 'me'
    sender_name TEXT NOT NULL,                  -- 'Me' or Contact Name
    is_from_me BOOLEAN NOT NULL DEFAULT FALSE,
    sent_at TIMESTAMPTZ NOT NULL,               -- Converted from libgm microseconds
    body TEXT NOT NULL DEFAULT '',
    reply_to_message_id TEXT,
    reactions JSONB NOT NULL DEFAULT '[]',      -- [{sender: "Her", emoji: "❤️"}]
    raw_status INT,                             -- BugleStatus enum
    is_backfill BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_messages_conv_sent ON messages (conversation_id, sent_at DESC);
CREATE INDEX IF NOT EXISTS idx_messages_unassigned ON messages (conversation_id, sent_at ASC) WHERE session_id IS NULL;

-- 4. Media Attachments & Vision OCR
CREATE TABLE IF NOT EXISTS media_attachments (
    id BIGSERIAL PRIMARY KEY,
    message_id TEXT NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    sha256_hash TEXT NOT NULL,
    relative_path TEXT NOT NULL,                -- e.g., "ab/cd/<sha256>.jpg"
    mime_type TEXT NOT NULL,
    file_size_bytes BIGINT NOT NULL DEFAULT 0,
    width INT,
    height INT,
    ocr_text TEXT,                              -- Verbatim text extracted from screenshot/image
    visual_description TEXT,                    -- Semantic summary of image contents
    vision_processed BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (message_id, sha256_hash)
);

CREATE INDEX IF NOT EXISTS idx_media_conv ON media_attachments (conversation_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_media_unprocessed ON media_attachments (conversation_id) WHERE vision_processed = FALSE;

-- 5. Living Dossiers (Current State per Tracked Contact)
CREATE TABLE IF NOT EXISTS contact_dossiers (
    conversation_id TEXT PRIMARY KEY REFERENCES conversations(id) ON DELETE CASCADE,
    contact_name TEXT NOT NULL,
    markdown_content TEXT NOT NULL,
    structured_state JSONB NOT NULL DEFAULT '{}', -- Upcoming events, people, open_loops
    last_processed_message_at TIMESTAMPTZ,
    version INT NOT NULL DEFAULT 1,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 6. Dossier Audit History (Allows rollback if LLM hallucinates or drops state)
CREATE TABLE IF NOT EXISTS contact_dossier_history (
    id BIGSERIAL PRIMARY KEY,
    conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    version INT NOT NULL,
    markdown_content TEXT NOT NULL,
    structured_state JSONB NOT NULL,
    trigger_session_id BIGINT REFERENCES conversation_sessions(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
