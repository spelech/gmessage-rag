-- Migration 002: durable application settings.
-- Stores key/value settings that survive restarts, including the
-- hot-reloadable LLM model selection per pipeline role.

CREATE TABLE IF NOT EXISTS app_settings (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
