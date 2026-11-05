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

## Connection Scheduling (`SYNC-01`)

`SYNC_MODE` selects how the daemon holds the Google Messages session. Real-time
ingestion requires a live connection; periodic mode connects only during a short
window so the phone is not held "connected" all day.

| Variable | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `SYNC_MODE` | string | `continuous` | `continuous` (always connected) or `periodic` (connect per window) |
| `SYNC_INTERVAL_MINUTES` | int | `30` | Minutes between the start of one periodic sync round and the next |
| `SYNC_WINDOW_MINUTES` | int | `5` | Minutes the connection stays open each round before disconnecting |
| `GM_PING_INTERVAL_MINUTES` | int | `5` | Ditto ping interval (libgm accepts 1..240) |
| `GM_ALERT_TIMEOUT_COUNT` | int | `2` | Missed pings before the phone is marked unresponsive |

Disconnecting does not clear the session cookie, so the same `session.json`
resumes on the next round. In periodic mode the daemon runs one historical
backfill pass at startup and then a forward drain per window.

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
| `ENABLE_MCP_SEND_MESSAGE` | bool | `false` | Fail-closed guard: registers the outbound `send_message` tool only when `true` and a paired sender is wired (`MCP-07`) |
| `NOTIFY_ON_PHONE_DISCONNECT` | bool | `false` | Sends push alerts on phone disconnect |
| `NOTIFY_PHONE_DISCONNECT_GRACE_MINUTES` | int | `15` | Debounce window before alerting on disconnect |

## Testing & Live Verification (HARN-03)

The default test run is **hermetic**. The `internal/gm` package installs a deny-all HTTP
transport through the `libgm` constructor seam, so any accidental outbound request fails
the run with a `hermetic network guard (HARN-03)` error.

| Command | Purpose | Network |
| :--- | :--- | :--- |
| `make test` | Hermetic unit, boundary, and race suite | **Blocked** |
| `make test-live` | Real Google registration / Gaia pairing smoke tests | Required |
| `make test-live-compile` | Compile-checks the `live`-tagged tests without running them | Not used |

**Test-only environment variables** (never required at runtime):

- `GM_ALLOW_NETWORK=1` — disables the hermetic guard so live tests can reach the network.
- `LIVE_GM_TEST=1` — explicit opt-in gate for tests tagged `//go:build live`.
- `SESSION_PATH` — session file used by the live pairing test (avoids clobbering the daemon session).
