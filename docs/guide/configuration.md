# Configuration Reference

All settings can be specified via environment variables or a `.env` file.

## Core Settings

| Variable | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `DATABASE_URL` | string | `postgres://...` | Connection URL for PostgreSQL 17 + pgvector |
| `HTTP_PORT` | int | `8090` | HTTP port for REST, Web UI, and MCP endpoints |
| `AUTH_DIR` | path | `/data/auth` | Directory storing session token JSON files |
| `MEDIA_DIR` | path | `/data/media` | Root content-addressed store for media attachments |
| `DOSSIER_DIR` | path | `/data/dossiers` | Directory where Markdown dossiers are saved |

## Ingestion & Pipeline Settings

| Variable | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `DEBOUNCER_IDLE_MINUTES` | int | `30` | Idle silence before triggering reconciliation |
| `ENABLE_READ_RECEIPT_HEURISTIC` | bool | `true` | Inferred delivery receipts for SMS threads |
| `PULL_FULL_SIZE_MEDIA` | bool | `false` | Request full-resolution media before OCR |
| `BACKFILL_BATCH_SIZE` | int | `100` | Pagination batch size for historical backfill |

## Model & Vision Settings

| Variable | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `OPENAI_API_KEY` | string | `""` | API key for OpenAI / OpenRouter endpoints |
| `OPENAI_BASE_URL` | string | `https://api.openai.com/v1` | OpenAI-compatible endpoint URL |
| `VISION_MODEL` | string | `gpt-4o-mini` | Vision model used for image OCR and descriptions |
| `EMBEDDING_MODEL` | string | `text-embedding-3-small` | 1536-dimension embedding model for sessions |

## MCP & Security Settings

| Variable | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `ENABLE_MCP_SEND_MESSAGE` | bool | `false` | Fail-closed guard: enables outbound `send_message` tool |
| `NOTIFY_ON_PHONE_DISCONNECT` | bool | `false` | Sends push alerts on phone disconnect |
| `NOTIFY_PHONE_DISCONNECT_GRACE_MINUTES` | int | `15` | Debounce window before alerting on disconnect |
