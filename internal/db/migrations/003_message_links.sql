-- Migration 003: message and session causal links.
-- Supports antecedent resolution, open loop tracking, and narrative hopping.

CREATE TABLE IF NOT EXISTS message_links (
    id BIGSERIAL PRIMARY KEY,
    conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,

    -- Source session (the newer session that references or resolves an antecedent)
    source_session_id BIGINT NOT NULL REFERENCES conversation_sessions(id) ON DELETE CASCADE,
    -- Target session (the historical session where the topic/open loop originated)
    target_session_id BIGINT NOT NULL REFERENCES conversation_sessions(id) ON DELETE CASCADE,

    -- Optional message-level precision when available (e.g. from explicit reply_to_message_id)
    source_message_id TEXT REFERENCES messages(id) ON DELETE CASCADE,
    target_message_id TEXT REFERENCES messages(id) ON DELETE CASCADE,

    -- Link classification:
    -- 'explicit_reply': direct reply_to_message_id from protocol
    -- 'open_loop_resolution': session resolves or closes an open loop from target session
    -- 'antecedent_continuation': session references a topic, event, or entity from target session
    link_type TEXT NOT NULL,

    -- Confidence score between 0.000 and 1.000 (1.0 for explicit protocol replies)
    confidence NUMERIC(4,3) NOT NULL DEFAULT 1.000,

    -- Structured evidence detailing why this link was established
    -- e.g. {"open_loop_id": "dr_appointment", "anchor_phrase": "that doctor", "reason": "Followup on Dr. Smith consultation"}
    evidence JSONB NOT NULL DEFAULT '{}',

    -- Audit status
    status TEXT NOT NULL DEFAULT 'detected',
    created_by TEXT NOT NULL DEFAULT 'system',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- Prevent self-links
    CONSTRAINT chk_no_self_session_link CHECK (source_session_id <> target_session_id)
);

CREATE INDEX IF NOT EXISTS idx_message_links_conv_source
    ON message_links (conversation_id, source_session_id);

CREATE INDEX IF NOT EXISTS idx_message_links_conv_target
    ON message_links (conversation_id, target_session_id);

CREATE INDEX IF NOT EXISTS idx_message_links_status
    ON message_links (conversation_id, status);
