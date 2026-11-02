# Software Requirements Specification: gmessages-rag

This specification defines the functional requirements, category taxonomy, and automated test traceability matrix for **`gmessages-rag`** (Real-Time Google Messages RCS/SMS Ingestion, Vision OCR, Living Dossier & Hybrid RAG MCP Server).

---

## Requirement Category Taxonomy

Requirement identifiers use `<CATEGORY>-<NN>` format (`AUTH-01`, `DB-01`, `GUARD-01`, `MCP-01`, `SEC-01`, `TRANS-01`, `PIPE-01`, `UI-01`, `NOTIF-01`, `OBS-01`, `HARN-01`, `CLEANUP-01`, `DOCKER-01`, `DAEMON-01`, `REFACTOR-01`). Do not use generic `REQ-` prefixes.

| Category Code | Domain Scope | Proof Types |
| :--- | :--- | :--- |
| **`AUTH`** | Google Messages Web pairing (QR terminal, Web QR) & session token lifecycle | `PositiveFeature` / `FailClosedGuardrail` |
| **`DB`** | PostgreSQL 17 + pgvector migrations, message upserts, session chunking, and RRF queries | `PositiveFeature` / `FailClosedGuardrail` |
| **`GUARD`** | Fail-closed guardrails, untrusted conversation XML tagging, send-message restrictions | `FailClosedGuardrail` |
| **`MCP`** | Model Context Protocol tool routing, schema validation, Streamable HTTP/SSE & stdio transport | `PositiveFeature` / `FailClosedGuardrail` |
| **`SEC`** | Content-addressed media storage, session token file permissions (`0600`), secret isolation | `FailClosedGuardrail` |
| **`TRANS`** | Web server (`/health`, `/pair`, `/api/v1/*`), Home Assistant briefing API, outbound webhooks | `PositiveFeature` / `FailClosedGuardrail` |
| **`PIPE`** | Vision OCR sidecar, 2-hour sessionizer, 30m idle debouncer, and Living Dossier LLM reconciliation | `PositiveFeature` / `FailClosedGuardrail` |
| **`UI`** | Web Management Dashboard, parameter tuner, live test bench & pairing console | `PositiveFeature` / `FailClosedGuardrail` |
| **`NOTIF`** | Universal multi-channel push notifications (Apprise & ntfy) and watchdog monitoring | `PositiveFeature` / `FailClosedGuardrail` |
| **`OBS`** | Observability, Prometheus metrics registry (`/metrics`), and health check probes (`/healthz`) | `PositiveFeature` / `FailClosedGuardrail` |
| **`HARN`** | Controls-grade in-memory simulation test harness and disturbance injection suite | `PositiveFeature` / `FailClosedGuardrail` |
| **`DOCKER`**| Container orchestration, multi-stage build, PUID/PGID privilege drop, volume persistence | `PositiveFeature` / `FailClosedGuardrail` |
| **`DAEMON`**| End-to-end daemon wiring, unified ServeMux routing, graceful shutdown orchestration | `PositiveFeature` / `FailClosedGuardrail` |
| **`CLEANUP`**| Code smell elimination, error wrapping `%w`, goroutine leak validation (`goleak`) | `NonFunctional` |
| **`REFACTOR`**| Comprehensive architecture refactoring and structural smell purge | `NonFunctional` |
| **`INT`** | External ecosystem integrations (Home Assistant MQTT bridge - held/deferred) | `PositiveFeature` |

---

## Requirements and Traceability Matrix

| Requirement ID | Category | Status | Summary | Merged PR / Commit | Acceptance Criteria | Automated Test Proof |
| :--- | :--- | :---: | :--- | :--- | :--- | :--- |
| **`AUTH-01`** | `AUTH` | [x] Complete | **Session Token Persistence** | Commit `7456168` (PR #7) | Atomically writes `*libgm.AuthData` to `/data/auth/session.json` with `0600` permissions on `PairSuccessful` and `AuthTokenRefreshed`. | `internal/gm/pairing_test.go::TestSessionPersistence_Permissions` |
| **`AUTH-02`** | `AUTH` | [x] Complete | **Dual Pairing Workflow** | Commit `7456168` (PR #7) | Supports terminal QR printing (`gmessages-rag pair`) and web UI QR rendering (`GET /pair`) with live refresh. | `internal/gm/pairing_test.go::TestPairingHandlers` |
| **`AUTH-03`** | `AUTH` | [x] Complete | **Push Alerting & Remote Re-Auth** | Commit `7456168` (PR #35) | Dispatches Apprise/ntfy push notifications when token refresh fails or session is revoked, with direct link to `/pair` web UI for mobile re-pairing. | `internal/notify/client_test.go::TestPushAlerting_ReAuthNotification` |
| **`AUTH-04`** | `AUTH` | [x] Complete | **Single-Device Gaia Pairing (No-QR Flow)** | PR #36, commit `fd1f7a3` | Supports Google Account (Gaia) pairing via `DoGaiaPairing`: displays matching emoji challenge on web UI (`GET /pair/gaia`) or sends via Apprise push for single-device pairing without camera QR scanning. | `internal/gm/gaia_pairing_test.go::TestGaiaPairing_Success` |
| **`DB-01`** | `DB` | [x] Complete | **Schema Migration & Vector Init** | PR #4, #5, #7, #13, #15, #16 | Automatically applies embedded `001_init.sql` creating `conversations`, `conversation_sessions` (with `halfvec(1536)` HNSW), `messages`, `media_attachments`, and `contact_dossiers`. | `internal/db/db_test.go::TestDatabaseMigrations` |
| **`DB-02`** | `DB` | [x] Complete | **Message & Reaction Ingestion** | PR #4, #5, #7, #13, #15, #16 | Normalizes microsecond/millisecond timestamps to UTC, resolves incoming/outgoing direction, and idempotently upserts messages and emoji reactions. | `internal/db/messages_test.go::TestUpsertMessages_Idempotent` |
| **`DB-03`** | `DB` | [x] Complete | **2-Hour Session Windowing** | PR #4, #5, #7, #13, #15, #16 | Groups messages separated by $\le 2\text{ hours}$ into `conversation_sessions` with absolute calendar headers and inline media OCR annotations. | `internal/db/sessions_test.go::TestSessionGrouping_2HourGap` |
| **`DB-04`** | `DB` | [x] Complete | **Hybrid RRF Search Query** | PR #4, #5, #7, #13, #15, #16 | Combines full-text BM25 (`ts_rank_cd`) and vector cosine similarity (`<=>`) using Reciprocal Rank Fusion ($k=60$) over session transcripts. | `internal/db/sessions_test.go::TestSearchSessionsHybrid_RRF` |
| **`DB-05`** | `DB` | [x] Complete | **Living Dossier State & Audit History** | PR #4, #5, #7, #13, #15, #16 | Upserts current structured state in `contact_dossiers` and snapshots prior state to `contact_dossier_history` for rollback safety. | `internal/db/dossiers_test.go::TestDossierVersionAuditHistory` |
| **`DB-06`** | `DB` | [x] Complete | **Inferred Read/Delivery Heuristics** | PR #37, commit `0c88b51` | When enabled (`ENABLE_READ_RECEIPT_HEURISTIC=true`), infers prior outgoing messages up to incoming reply timestamp are delivered and acknowledged for SMS/iPhone contacts lacking native receipts. | `internal/db/messages_test.go::TestUpsertMessages_ReadReceiptHeuristic` |
| **`GUARD-01`** | `GUARD` | [x] Complete | **Prompt Injection XML Boundary** | Commit `57dad5e` | All raw message texts, transcripts, and OCR text returned by MCP tools are wrapped in `<untrusted_conversation_data>` tags. | `internal/mcpserver/guardrails_test.go::TestWrapUntrustedData_Escaping` |
| **`GUARD-02`** | `GUARD` | [x] Complete | **Send-Message Restriction** | Commit `57dad5e` | Deliberately omits outbound `send_message` MCP tool unless `ENABLE_MCP_SEND_MESSAGE=true` is explicitly configured. | `internal/mcpserver/tools_test.go::TestSendMessageTool_RegistrationGuard` |
| **`PIPE-01`** | `PIPE` | [x] Complete | **Vision OCR Sidecar** | PR #6, #12 | Extracts verbatim OCR text and 1-2 sentence visual description for image attachments in tracked conversations via OpenAI-compatible vision model. | `internal/pipeline/vision_test.go::TestVisionOCR_Extraction` |
| **`PIPE-02`** | `PIPE` | [x] Complete | **Per-Conversation Idle Debouncer** | PR #6, #12 | Maintains thread-safe timers per tracked conversation; triggers pipeline run after 30 minutes of messaging silence. | `internal/pipeline/debouncer_test.go::TestDebouncer_ResetOnNewMessage` |
| **`PIPE-03`** | `PIPE` | [x] Complete | **LLM State Reconciliation Engine** | PR #34, commit `1141504` | Reconciles new sessions against current dossier: anchors relative dates, applies chronological overrides, transitions past events, and exports Markdown to disk. | `internal/pipeline/dossier_test.go::TestDossierReconciliation_DateAnchoring` |
| **`PIPE-04`** | `PIPE` | [x] Complete | **Google Contacts Enrichment** | PR #47, commit `836834b` | Pulls `ListContacts` / `ListTopContacts` to enrich dossiers with contact names, avatars, phone labels, and relationship metadata. | `internal/pipeline/contacts_test.go::TestContactsSync_DossierEnrichment` |
| **`PIPE-05`** | `PIPE` | [x] Complete | **Action-Aware Smart Debouncing** | PR #40, commit `09c2323` | Listens to participant typing events (`UpdateEvents_TypingEvent`) and outbound drafts to reset or pause the 30-minute idle window dynamically. | `internal/pipeline/debouncer_test.go::TestDebouncer_TypingIndicatorReset` |
| **`PIPE-06`** | `PIPE` | [x] Complete | **Standardized Batch ETL Ingestion** | PR #48, commit `badf3e9` | Staged pipeline for long conversation backfills: chunked cursor fetching, normalized transformation, and batched pgvector staging with bounded memory usage. | `internal/pipeline/etl_test.go::TestETLPipeline_BatchProcessing` |
| **`OBS-01`** | `OBS` | [x] Complete | **Prometheus Metrics & Health Probes** | PR #41, commit `151d981` | Exposes standard Prometheus metrics (`/metrics`) and health check probes (`/healthz`) with database pool reachability and WebSocket listener status. | `internal/obs/metrics_test.go::TestMetricsRegistrationAndScrape` |
| **`NOTIF-01`** | `NOTIF` | [x] Complete | **Apprise & ntfy Push Alerting** | PR #35, commit `796debd` | Dispatches high-priority push notifications via Apprise and ntfy endpoints with embedded action links for mobile re-auth and connection events. | `internal/notify/client_test.go::TestPushAlerting_ReAuthNotification` |
| **`NOTIF-02`** | `NOTIF` | [x] Complete | **Debounced Connection Watchdog** | PR #35, commit `796debd` | Monitors `PhoneNotResponding` and suppresses alerts during configurable grace periods (default 15m) to eliminate transient cell drop fatigue. | `internal/notify/watchdog_test.go::TestWatchdog_PhoneDisconnect_GracePeriodTrigger` |
| **`SEC-01`** | `SEC` | [x] Complete | **Content-Addressed Media Decryption** | PR #34, commit `1141504` | Decrypts RCS attachments, computes SHA-256 hash, and stores files at `/data/media/<h1h2>/<h3h4>/<hash>.<ext>` with duplicate avoidance. | `internal/gm/media_test.go::TestMediaDownload_SHA256Pathing` |
| **`SEC-02`** | `SEC` | [x] Complete | **Full-Resolution Media Retrieval** | PR #38, commit `8d5b7e4` | When opted-in (`PULL_FULL_SIZE_MEDIA=true`), requests full-resolution image assets via `GetFullSizeImage` before running Vision OCR (default: progressive thumbnail). | `internal/gm/media_test.go::TestMediaDownload_FullSizeOption` |
| **`SEC-03`** | `SEC` | [x] Complete | **CI Vulnerability Scanning & Race Checks** | Issue #30, commit `30b0cb6` | Enforces `govulncheck` static call-graph vulnerability scanning and `go test -race` concurrency validation in CI and Makefile targets. | GitHub Actions CI & `Makefile::vulncheck` |
| **`HARN-01`** | `HARN` | [x] Complete | **Protobuf Disturbance Simulation Fixture** | Issue #29, commit `ecac8e2` | In-memory Google Messages WebSocket simulation server injecting synthetic protobuf frames (disconnects, media bursts, typing events, payload corruption) for deterministic CI verification. | `internal/gm/testing/mock_server_test.go::TestMockGMServer_DisturbanceRecovery` |
| **`HARN-02`** | `HARN` | [ ] In Progress | **In-Process libgm Protocol Emulator** | Issue #62 | Hermetic, in-process implementation of the Google Messages RPC surface, wired through the `exhttp.ClientSettings.TransportOverride` injection point (no libgm fork). First slice covers the pairing handshake (`RegisterPhoneRelay`, `/web/config`). | `internal/gm/emulator_e2e_test.go::TestEmulator_StartLoginReturnsQR` |
| **`HARN-03`** | `HARN` | [x] Complete | **Hermetic Test Suite & Live Network Quarantine** | Issue #63 | Default `go test ./...` runs fully offline: `internal/gm` installs a deny-all HTTP transport via the `libgm` constructor seam. Live-network tests are quarantined behind the `live` build tag and `LIVE_GM_TEST=1`. | `internal/gm/hermetic_test.go::TestHermeticGuard_BlocksOutboundRegistration` |
| **`UI-01`** | `UI` | [x] Complete | **Web Management Dashboard** | PR #46, commit `8dfb444` | Embedded SPA providing live daemon status, database connectivity metrics, tracked contact toggle table, and pairing QR visualizer. | `internal/web/ui_test.go::TestUI_DashboardRendering` |
| **`UI-02`** | `UI` | [x] Complete | **Interactive Test Bench (Context-Cortex style)** | PR #45, commit `91e8fcc` | Developer harness in web UI to execute ad-hoc Hybrid RRF queries, test MCP tools with live responses, inspect XML boundaries, and review dossier history diffs. | `internal/web/testbench_test.go::TestUI_TestBenchExecution` |
| **`UI-03`** | `UI` | [x] Complete | **Parameter & Pipeline Tuner** | PR #44, commit `2407ed6` | Web interface to view and hot-tune pipeline parameters (debouncer idle duration, RRF $k$ factor, OCR model endpoint, backfill window). | `internal/config/tuner_test.go::TestRuntimeTuner_ConcurrentUpdatesAndNotification` |
| **`UI-05`** | `UI` | [x] Complete | **Embedded Test Bench & Tuner in Dashboard** | PR #49 | Integrates UI-02 (Diagnostic Test Bench) and UI-03 (Runtime Tuner) into tabbed navigation in index.html with interactive MCP tools, hybrid RRF search, guardrail sanitizer, and hot-tuning REST endpoints. | `internal/web/dashboard_test.go::TestDashboardUI_AssetServing` |
| **`CLEANUP-01`**| `CLEANUP` | [x] Complete | **Dead Code & Error Handling Hardening** | Issue #42, commit `c279ebd` | Purged redundant models, eliminated ignored errors, guaranteed strict error propagation on file operations, and added `goleak` goroutine lifecycle validation. | Full package test suite & `go test -race ./...` |
| **`DOCKER-01`** | `DOCKER` | [x] Complete | **Hardened Non-Root Container & PUID/PGID** | Issue #42, commit `c279ebd` | Hardened multi-stage non-root container with `PUID`/`PGID` permission handling, `.env.example` alignment, and immutable volume mounts. | `Dockerfile`, `docker-compose.yml` |
| **`DAEMON-01`** | `DAEMON` | [x] Complete | **Unified End-to-End Daemon Orchestration** | Issue #42, commit `c279ebd` | Orchestrated root `ServeMux` linking `/`, `/api/*`, `/testbench`, `/mcp`, `/metrics`, and `/healthz` with graceful SIGINT/SIGTERM shutdown and background ETL sync. | `cmd/gmessages-rag/serve_test.go::TestBuildServeMux_Wiring` |
| **`REFACTOR-01`**| `REFACTOR`| [x] Complete | **Comprehensive Architecture Purge** | Issue #42, commit `c279ebd` | Holistic code smell elimination, package clean boundaries, and zero-theatre controls verification across all daemon subsystems. | Full repository test suite |
| **`MCP-00`** | `MCP` | [x] Complete | **2026-07-28 Spec & Ping Support** | Commit `57dad5e` | Strictly adheres to the 2026-07-28 MCP specification: returns `Mcp-Protocol-Version: 2026-07-28` header, handles `ping` JSON-RPC method returning empty object `{}`, supports `initialize`/`notifications/initialized`, and runs over Streamable HTTP (`POST /mcp`), SSE (`GET /sse`), and stdio. | `internal/mcpserver/server_test.go::TestProtocol_20260728_PingAndHandshake` |
| **`MCP-01`** | `MCP` | [x] Complete | **`list_conversations` Tool** | Commit `57dad5e` | Lists conversations with IDs, contact names, participant identifiers, tracked status, and last activity timestamp. | `internal/mcpserver/tools_test.go::TestTool_ListConversations` |
| **`MCP-02`** | `MCP` | [x] Complete | **`get_contact_dossier` Tool** | Commit `57dad5e` | Returns current Markdown dossier, structured JSON state, plus live tail of unindexed messages sent after last dossier reconciliation. | `internal/mcpserver/tools_test.go::TestTool_GetContactDossier` |
| **`MCP-03`** | `MCP` | [x] Complete | **`search_conversation_history` Tool** | Commit `57dad5e` | Executes Hybrid RRF query across sessions with optional date bounds and returns formatted transcripts. | `internal/mcpserver/tools_test.go::TestTool_SearchConversationHistory` |
| **`MCP-04`** | `MCP` | [x] Complete | **`get_messages_around_time` Tool** | Commit `57dad5e` | Pulls verbatim message context and media OCR annotations within $\pm \text{window\_minutes}$ of a target timestamp. | `internal/mcpserver/tools_test.go::TestTool_GetMessagesAroundTime` |
| **`MCP-05`** | `MCP` | [x] Complete | **`search_shared_media` Tool** | Commit `57dad5e` | Searches media attachments by OCR text and visual descriptions. | `internal/mcpserver/tools_test.go::TestTool_SearchSharedMedia` |
| **`MCP-06`** | `MCP` | [x] Complete | **`manage_tracked_contact` Tool** | Commit `57dad5e` | Enables/disables thread tracking, initiates historical pagination backfill, or forces immediate dossier refresh. | `internal/mcpserver/tools_test.go::TestTool_ManageTrackedContact` |
| **`TRANS-01`** | `TRANS` | [x] Complete | **Health Check Probe** | Commit `57dad5e` / PR #41, commit `151d981` | Exposes `/health` and `/healthz` returning HTTP 200 with database connectivity status and version JSON. | `internal/web/server_test.go::TestHealthEndpoint_ReturnsHealthy` |
| **`TRANS-02`** | `TRANS` | [x] Complete | **Streamable MCP & SSE Endpoints** | Commit `57dad5e` | Exposes Streamable HTTP `/mcp` and Server-Sent Events `/sse` on port 8090. | `internal/web/server_test.go::TestMCPEndpoints_HTTPAndSSE` |
| **`TRANS-03`** | `TRANS` | [x] Complete | **Briefing API & Outbound Webhook** | PR #6, #12 / Commit `7456168` | Exposes `/api/v1/briefing/today` for Home Assistant REST sensors and dispatches POST to `WEBHOOK_URL` when dossiers update. | `internal/web/webhook_test.go::TestWebhookDispatch_OnDossierUpdate` |
| **`TRANS-04`** | `TRANS` | [x] Complete | **Configurable Diagnostics Alerts** | PR #35, commit `796debd` | Monitors phone responsiveness (`PhoneNotResponding`), but alerts only if configured (`NOTIFY_ON_PHONE_DISCONNECT=true`) and debounced by $N$ minutes. | `internal/notify/watchdog_test.go::TestWatchdog_PhoneDisconnect_GracePeriodTrigger` |
| **`INT-01`** | `INT` | [ ] Deferred | **Home Assistant MQTT Bridge** | Held (Issue #32) | Supplements outbound webhooks with an optional MQTT client bridge. **Note:** Deferred/held in favor of universal Apprise/ntfy push alerting and Prometheus metrics. | `internal/mqtt` baseline |

---

## Detailed Requirement Definitions

### `AUTH-01`: Session Token Persistence
- **Category**: `AUTH` (`PositiveFeature`)
- **Status**: `[x] Complete` (Commit `7456168`, PR #7)
- **Description**: Atomically write updated session credentials to `/data/auth/session.json.tmp` with POSIX file mode `0600` and rename to `/data/auth/session.json` upon `*events.PairSuccessful` and `*events.AuthTokenRefreshed`.
- **Verification**: Verify file mode is strictly `0600` and contents contain valid token state.

### `AUTH-02`: Dual Pairing Workflow
- **Category**: `AUTH` (`PositiveFeature`)
- **Status**: `[x] Complete` (Commit `7456168`, PR #7)
- **Description**: Supports terminal QR printing (`gmessages-rag pair`) and web UI QR rendering (`GET /pair`) with live refresh.
- **Verification**: Terminal rendering tests and HTTP handler tests asserting SVG/ANSI QR generation.

### `AUTH-03`: Push Alerting & Remote Re-Auth
- **Category**: `AUTH` (`PositiveFeature`)
- **Status**: `[x] Complete` (Commit `7456168`, PR #35)
- **Description**: When Google session tokens fail to refresh, browser pairing is revoked, or authentication unlinks, the daemon sends an alert via Apprise/ntfy with a direct link (or embedded action) to `/pair` on the Web Management UI, allowing users to approve/scan pairing directly from their mobile phone or remote browser without container terminal access.
- **Verification**: Unit tests verifying alert dispatch payload construction and URL endpoint formatting.

### `AUTH-04`: Single-Device Gaia Pairing (No-QR Flow)
- **Category**: `AUTH` (`PositiveFeature`)
- **Status**: `[x] Complete` (PR #36, commit `fd1f7a3`)
- **Description**: Solves the single-device pairing dilemma when no second screen is available. Instead of scanning a camera QR code, `gmessages-rag` leverages Google Messages' native Google Account (Gaia) pairing protocol:
  1. The daemon triggers `libgm.Client.DoGaiaPairing` (or `StartGaiaPairing`), which requests an interactive challenge from Google.
  2. Google Messages generates a pairing emoji challenge (e.g. 🐶 / 🚀 / ☕).
  3. The daemon renders this emoji challenge on `EXTERNAL_WEB_URL/pair/gaia` or pushes the emoji name directly to the user's phone via an Apprise/ntfy push notification.
  4. The user taps the notification on their phone, opens Google Messages, and simply taps the matching emoji on their screen to grant instant pairing approval without using a camera.
- **Verification**: Unit tests in `internal/gm/gaia_pairing_test.go` asserting emoji callback invocation, Ukey2 handshake processing, and `PairSuccessful` emission upon challenge confirmation.

### `DB-01`: Schema Migration & Vector Init
- **Category**: `DB` (`PositiveFeature`)
- **Status**: `[x] Complete` (PR #4, #5, #7, #13, #15, #16)
- **Description**: Automatically applies embedded `001_init.sql` creating `conversations`, `conversation_sessions` (with `halfvec(1536)` HNSW), `messages`, `media_attachments`, and `contact_dossiers`.
- **Verification**: Integration test running against Postgres container validating table DDL, index creation, and vector extensions.

### `DB-02`: Message & Reaction Ingestion
- **Category**: `DB` (`PositiveFeature`)
- **Status**: `[x] Complete` (PR #4, #5, #7, #13, #15, #16)
- **Description**: Normalizes microsecond/millisecond timestamps to UTC, resolves incoming/outgoing direction, and idempotently upserts messages and emoji reactions.
- **Verification**: `internal/db/messages_test.go::TestUpsertMessages_Idempotent` asserting zero duplicates on replay.

### `DB-03`: 2-Hour Session Windowing
- **Category**: `DB` (`PositiveFeature`)
- **Status**: `[x] Complete` (PR #4, #5, #7, #13, #15, #16)
- **Description**: Groups messages separated by $\le 2\text{ hours}$ into `conversation_sessions` with absolute calendar headers and inline media OCR annotations.
- **Verification**: Window calculation tests validating boundary splitting when time delta exceeds 2 hours.

### `DB-04`: Hybrid RRF Search Query
- **Category**: `DB` (`PositiveFeature`)
- **Status**: `[x] Complete` (PR #4, #5, #7, #13, #15, #16)
- **Description**: Implement a PostgreSQL Common Table Expression (CTE) fusing full-text search (`search_tsv @@ websearch_to_tsquery('english', $2)`) and vector cosine similarity (`embedding <=> $5::halfvec`) via Reciprocal Rank Fusion ($k=60$).
- **Verification**: Query execution tests asserting correct RRF scoring and rank fusion ordering.

### `DB-05`: Living Dossier State & Audit History
- **Category**: `DB` (`PositiveFeature`)
- **Status**: `[x] Complete` (PR #4, #5, #7, #13, #15, #16)
- **Description**: Upserts current structured state in `contact_dossiers` and snapshots prior state to `contact_dossier_history` for rollback safety.
- **Verification**: History audit tests verifying incremental versioning and immutable snapshots.

### `DB-06`: Inferred Read/Delivery Heuristics
- **Category**: `DB` (`PositiveFeature`)
- **Status**: `[x] Complete` (PR #37, commit `0c88b51`)
- **Description**: For SMS or iPhone RCS threads where explicit delivery or read receipts are omitted by the carrier or client, when `ENABLE_READ_RECEIPT_HEURISTIC=true` is set, any incoming message from a contact automatically causes all preceding outgoing messages in that conversation up to the incoming message timestamp to be marked as delivered and acknowledged.
- **Verification**: Ingestion tests with simulated interleaved incoming and outgoing messages verifying status progression.

### `GUARD-01`: Prompt Injection XML Boundary
- **Category**: `GUARD` (`FailClosedGuardrail`)
- **Status**: `[x] Complete` (Commit `57dad5e`)
- **Description**: To prevent indirect prompt injection from SMS/RCS contents, wrap all external conversational text in `<untrusted_conversation_data warning="...">` XML tags before presenting it to agent LLMs.
- **Verification**: Unit tests verifying that all MCP tool responses wrap text, transcript, and OCR outputs in the specified XML tags with proper escaping.

### `GUARD-02`: Send-Message Restriction
- **Category**: `GUARD` (`FailClosedGuardrail`)
- **Status**: `[x] Complete` (Commit `57dad5e`)
- **Description**: Deliberately omits outbound `send_message` MCP tool unless `ENABLE_MCP_SEND_MESSAGE=true` is explicitly configured.
- **Verification**: Unit tests in `internal/mcpserver/tools_test.go` verifying tool registration exclusion when flag is false.

### `PIPE-01`: Vision OCR Sidecar
- **Category**: `PIPE` (`PositiveFeature`)
- **Status**: `[x] Complete` (PR #6, #12)
- **Description**: Extracts verbatim OCR text and 1-2 sentence visual description for image attachments in tracked conversations via OpenAI-compatible vision model.
- **Verification**: Mock vision server tests verifying JSON parsing and database persistence.

### `PIPE-02`: Per-Conversation Idle Debouncer
- **Category**: `PIPE` (`PositiveFeature`)
- **Status**: `[x] Complete` (PR #6, #12)
- **Description**: Maintains thread-safe timers per tracked conversation; triggers pipeline run after 30 minutes of messaging silence.
- **Verification**: Debouncer unit tests verifying timer reset on incoming messages.

### `PIPE-03`: LLM State Reconciliation Engine
- **Category**: `PIPE` (`PositiveFeature`)
- **Status**: `[x] Complete` (PR #34, commit `1141504`)
- **Description**: Reconciles new sessions against current dossier: anchors relative dates, applies chronological overrides, transitions past events, and exports Markdown to disk.
- **Verification**: Mock LLM reconciliation tests validating chronological overrides and Markdown serialization.

### `PIPE-04`: Google Contacts Enrichment
- **Category**: `PIPE` (`PositiveFeature`)
- **Status**: `[x] Complete` (PR #47, commit `836834b`)
- **Description**: Periodically calls `libgm.Client.ListContacts` and `ListTopContacts` to extract profile full names, contact avatars, relationship nicknames, and phone label types (mobile/work/home), enriching `contact_dossiers` with ground-truth contact information beyond RCS group subject strings.
- **Verification**: Integration test asserting contact metadata sync and dossier enrichment.

### `PIPE-05`: Action-Aware Smart Debouncing
- **Category**: `PIPE` (`PositiveFeature`)
- **Status**: `[x] Complete` (PR #40, commit `09c2323`)
- **Description**: Enhances the 30-minute idle debouncer by listening to participant typing indicators (`UpdateEvents_TypingEvent`) and outbound drafts/responses. If a participant starts typing or the user replies, the debouncer timer is automatically reset and held open to prevent premature session cutoffs mid-conversation.
- **Verification**: Timer lifecycle tests verifying reset on typing event injection.

### `PIPE-06`: Standardized Batch ETL Ingestion
- **Category**: `PIPE` (`PositiveFeature`)
- **Status**: `[x] Complete` (PR #48, commit `badf3e9`)
- **Description**: Implements a standardized three-stage ETL pipeline for historical backfill and thread ingestion:
  1. **Extract**: Cursor-paginated batch retrieval from `libgm.FetchMessages` with configurable batch sizes (e.g., 100 messages/batch).
  2. **Transform**: Timestamp normalization across magnitudes, direction resolution, read-receipt heuristics, and SHA-256 media deduction.
  3. **Load**: Transactional batch upsert into `messages` and atomic progression of `backfill_cursor`, avoiding memory bloat or table lock contention on threads exceeding 10,000 messages.
- **Verification**: Pipeline test verifying memory boundedness and cursor resumption across large simulated backfill datasets.

### `OBS-01`: Standardized Metrics & Observability Endpoints
- **Category**: `OBS` (`PositiveFeature`)
- **Status**: `[x] Complete` (PR #41, commit `151d981`)
- **Description**: Exposes runtime Prometheus metrics (`/metrics`) and health probes (`/healthz`):
  - Ingestion counters (`gmessages_messages_ingested_total`, `gmessages_media_bytes_decrypted_total`).
  - Pipeline gauges (`gmessages_active_websocket_connections`, `gmessages_session_chunks_created_total`).
  - Lightweight `/healthz` endpoint checking database pool reachability and WebSocket listener status for container orchestrators.
- **Verification**: HTTP handler tests verifying metric emission and health status checks.

### `NOTIF-01`: Apprise & ntfy Push Alerting
- **Category**: `NOTIF` (`PositiveFeature`)
- **Status**: `[x] Complete` (PR #35, commit `796debd`)
- **Description**: Multi-channel push notification dispatcher supporting both self-hosted ntfy servers and universal Apprise gateways. Emits high-priority mobile alerts with action buttons for QR/Gaia re-authentication and pairing approvals.
- **Verification**: Mock webhook dispatch tests verifying alert payloads and header tags.

### `NOTIF-02`: Debounced Connection Watchdog
- **Category**: `NOTIF` (`PositiveFeature`)
- **Status**: `[x] Complete` (PR #35, commit `796debd`)
- **Description**: Monitors `libgm` connection state transitions (`PhoneNotResponding`, `PhoneRespondingAgain`, `ListenFatalError`). Suppresses disconnect notifications during a configurable grace period (default 15m) to prevent alert fatigue during workplace cellular dips.
- **Verification**: Watchdog state tests validating alert suppression and reconnection recovery.

### `SEC-01`: Content-Addressed Media Decryption
- **Category**: `SEC` (`FailClosedGuardrail`)
- **Status**: `[x] Complete` (PR #34, commit `1141504`)
- **Description**: Decrypts RCS attachments, computes SHA-256 hash, and stores files at `/data/media/<h1h2>/<h3h4>/<hash>.<ext>` with duplicate avoidance.
- **Verification**: Attachment storage tests validating SHA-256 two-tier tree paths and idempotency.

### `SEC-02`: Full-Resolution Media Retrieval
- **Category**: `SEC` (`PositiveFeature`)
- **Status**: `[x] Complete` (PR #38, commit `8d5b7e4`)
- **Description**: When `PULL_FULL_SIZE_MEDIA=true` is enabled (default `false`), the media handler calls `libgm.Client.GetFullSizeImage` to download original high-resolution assets before invoking the Vision OCR pipeline, conserving bandwidth by default while providing crystal-clear OCR for users who require it.
- **Verification**: Attachment downloader tests asserting full-size fetch execution when flag is active vs thumbnail fallback.

### `SEC-03`: CI Vulnerability Scanning & Concurrency Race Detection
- **Category**: `SEC` (`NonFunctional`)
- **Status**: `[x] Complete` (Issue #30, commit `30b0cb6`)
- **Description**: Enforces automated security auditing and concurrency verification in CI:
  1. `govulncheck`: Static call-graph vulnerability scanning against the Go Vulnerability Database.
  2. `go test -race`: Concurrency race detection across all internal packages to guarantee data-race-free timer, channel, and connection operations.
- **Verification**: Mandatory CI gate steps failing on any detected call-graph CVE or data race.

### `HARN-01`: Controls-Grade Protobuf Disturbance Simulation Fixture
- **Category**: `HARN` (`PositiveFeature`)
- **Status**: `[x] Complete` (Issue #29, commit `ecac8e2`)
- **Description**: Provides an in-memory Google Messages WebSocket simulation harness capable of injecting raw protobuf disturbance frames (pairing challenges, sudden socket disconnections, bursts of concurrent RCS media attachments, rapid-fire typing indicators, corrupt chunks) to verify daemon resilience without physical hardware.
- **Verification**: Simulation suite running in CI with zero hardware dependencies, verifying deterministic recovery.

### `HARN-02`: In-Process libgm Protocol Emulator
- **Category**: `HARN` (`PositiveFeature`)
- **Status**: `[ ] In Progress` (Issue #62)
- **Description**: An in-repo, hermetic emulator for the Google Messages HTTP RPC surface that `libgm` speaks, wired through `exhttp.ClientSettings.TransportOverride`. No `libgm` fork and no sockets. The first slice covers the pairing handshake:
  1. `RegisterPhoneRelay` returns a valid `RegisterPhoneRelayResponse` (pairing key, token data, browser device).
  2. `/web/config` returns a `Config` payload.
  3. `ReceiveMessages` long-poll blocks until cancellation (event injection is a follow-up slice).
- **Remaining scope (Issue #62)**: `RefreshPhoneRelay`, `SignInGaia`/`RegisterRefresh`, `SendMessage`/`AckMessages`, `FetchMessages` cursor paging, `ListContacts`, media upload/download, and long-poll event injection (`PairSuccessful`, `WrappedMessage`).
- **Verification**: `internal/gm/emulator_e2e_test.go::TestEmulator_StartLoginReturnsQR` drives the real `libgm.StartLogin` through the emulator and asserts a QR challenge is produced with zero network access.

### `HARN-03`: Hermetic Test Suite & Live Network Quarantine
- **Category**: `HARN` (`FailClosedGuardrail`)
- **Status**: `[x] Complete` (Issue #63)
- **Description**: Enforces a fully offline default test run. The `gm` package injects a deny-all `http.RoundTripper` through the `libgm` constructor seam (`newLibGMClient`) unless `GM_ALLOW_NETWORK=1`. Any accidental outbound request fails loudly with a guard error. Live-network tests (real Google registration and Gaia pairing) are quarantined behind the `live` build tag and require `LIVE_GM_TEST=1`.
- **Verification**: `internal/gm/hermetic_test.go::TestHermeticGuard_BlocksOutboundRegistration` drives the real `libgm.StartLogin` path and asserts the guard blocks it. `TestDenyNetworkTransport_RejectsRequests` covers the transport directly. The hermetic suite runs in CI, and `make test-live-compile` validates the `live` build without executing it.

### `UI-01`: Web Management Dashboard
- **Category**: `UI` (`PositiveFeature`)
- **Status**: `[x] Complete` (PR #46, commit `8dfb444`)
- **Description**: Embedded web UI served on `/` showing daemon status, libgm pairing state (with live QR code rendering), PostgreSQL connectivity and row counts, tracked contact toggles, and recent event logs.
- **Verification**: Browser and handler tests asserting HTML rendering, JSON status polling, and pairing widget interactivity.

### `UI-02`: Interactive Test Bench (Context-Cortex style)
- **Category**: `UI` (`PositiveFeature`)
- **Status**: `[x] Complete` (PR #45, commit `91e8fcc`)
- **Description**: Developer diagnostic test bench providing interactive sandboxes to:
  1. Test all 6 MCP tools with arbitrary arguments and inspect formatted output (including `<untrusted_conversation_data>` boundaries).
  2. Run ad-hoc Hybrid RRF queries against ingested conversation sessions with vector vs full-text score breakdown.
  3. Simulate Vision OCR extraction on user-uploaded test images.
  4. Inspect Living Dossier structured JSON, rendered Markdown, and audit snapshot history diffs.
- **Verification**: Integration tests asserting test bench API handlers (`/api/v1/testbench/*`) execute queries, return raw & wrapped payloads, and provide accurate diagnostic output.

### `UI-03`: Parameter & Pipeline Tuner
- **Category**: `UI` (`PositiveFeature`)
- **Status**: `[x] Complete` (PR #44, commit `2407ed6`)
- **Description**: Web interface to inspect configuration and hot-tune operational parameters (such as debouncer idle window, RRF fusion $k$ factor, Vision OCR prompts, and backfill page limits) without restarting the container.
- **Verification**: Unit tests asserting parameter update endpoints persist runtime overrides and notify active workers.

### `UI-05`: Embedded Diagnostic Test Bench & Runtime Tuner
- **Category**: `UI` (`PositiveFeature`)
- **Status**: `[x] Complete` (PR #49)
- **Description**: Embeds interactive Diagnostic Test Bench (`UI-02`) and Runtime Tuner (`UI-03`) views into tabbed navigation in `internal/web/assets/index.html`. Operators can execute in-process MCP tools with JSON argument templates, benchmark hybrid RRF ranking, test guardrail XML escaping, and adjust runtime thresholds via `/api/v1/tuner` without restarting the daemon.
- **Verification**: Unit and asset integration tests in `internal/web/dashboard_test.go::TestDashboardUI_AssetServing`, `TestAPI_TunerEndpoints`, and `internal/web/testbench_test.go::TestTestBench_RouteIntegrationWithDashboard`.

### `CLEANUP-01`: Dead Code & Error Handling Hardening
- **Category**: `CLEANUP` (`NonFunctional`)
- **Status**: `[x] Complete` (Issue #42, commit `c279ebd`)
- **Description**: Comprehensive audit eliminating orphaned models, redundant helper utilities, unhandled errors, and improper error formatting across all packages. Enforces explicit `%w` error wrapping and verifies clean goroutine termination with `go.uber.org/goleak`.
- **Verification**: Zero lint warnings, clean `goleak` shutdown passes, and complete race-detector test suites.

### `DOCKER-01`: Hardened Non-Root Container & PUID/PGID
- **Category**: `DOCKER` (`PositiveFeature`)
- **Status**: `[x] Complete` (Issue #42, commit `c279ebd`)
- **Description**: Multi-stage hardened Alpine container image executing under a non-root service account (`nonroot:nonroot` or host-mapped `PUID`/`PGID`). Mounts persistent state partitions for `/data/auth`, `/data/media`, and `/data/dossiers` with strict POSIX permissions.
- **Verification**: Container startup verification validating non-root UID execution and bind mount writeability.

### `DAEMON-01`: Unified End-to-End Daemon Orchestration
- **Category**: `DAEMON` (`PositiveFeature`)
- **Status**: `[x] Complete` (Issue #42, commit `c279ebd`)
- **Description**: CLI commands (`cmd/gmessages-rag serve` and `pair`) wire all core subsystems into a unified `http.ServeMux` (`/`, `/api/*`, `/testbench`, `/mcp`, `/metrics`, `/healthz`), connect the connection watchdog, register action-aware debouncers, and manage graceful shutdown across `SIGINT`/`SIGTERM`.
- **Verification**: Integration test in `cmd/gmessages-rag/serve_test.go` asserting route dispatch and orderly subsystem shutdown.

### `REFACTOR-01`: Comprehensive Architecture Purge
- **Category**: `REFACTOR` (`NonFunctional`)
- **Status**: `[x] Complete` (Issue #42, commit `c279ebd`)
- **Description**: Holistic refactor addressing architectural technical debt accumulated across parallel agentic feature branches. Establishes clean decoupled interfaces, eliminates circular dependencies, and ensures high test cohesion.
- **Verification**: Repository-wide automated test suite execution without failures or regressions.

### `MCP-00`: 2026-07-28 Model Context Protocol Compliance & Ping Method
- **Category**: `MCP` (`PositiveFeature`)
- **Status**: `[x] Complete` (Commit `57dad5e`)
- **Description**: The MCP server strictly implements the `2026-07-28` Model Context Protocol specification:
  1. **Handshake & Version**: Handles `initialize` method returning `protocolVersion: "2026-07-28"`, capabilities, and server info. Handles `notifications/initialized`.
  2. **Ping Support**: Handles `ping` JSON-RPC method returning empty object `{}` with the request ID.
  3. **Protocol Header**: HTTP responses (both Streamable HTTP and SSE) return `Mcp-Protocol-Version: 2026-07-28`.
  4. **Multi-Transport**: Supports stateless Streamable HTTP (`POST /mcp`), Server-Sent Events (`GET /sse`), and standard I/O (`stdio`).
- **Verification**: Unit tests in `internal/mcpserver/server_test.go` asserting `ping` handling, `Mcp-Protocol-Version: 2026-07-28` response header, initialization lifecycle, and tool invocation.

### `MCP-01`: `list_conversations` Tool
- **Category**: `MCP` (`PositiveFeature`)
- **Status**: `[x] Complete` (Commit `57dad5e`)
- **Description**: Returns all known SMS/RCS conversations with identifiers, participant lists, tracked status, and last activity timestamps.
- **Verification**: Unit test in `internal/mcpserver/tools_test.go::TestTool_ListConversations`.

### `MCP-02`: `get_contact_dossier` Tool
- **Category**: `MCP` (`PositiveFeature`)
- **Status**: `[x] Complete` (Commit `57dad5e`)
- **Description**: Fetches the structured JSON state, Markdown narrative, and unindexed message tail for a tracked contact.
- **Verification**: Unit test in `internal/mcpserver/tools_test.go::TestTool_GetContactDossier`.

### `MCP-03`: `search_conversation_history` Tool
- **Category**: `MCP` (`PositiveFeature`)
- **Status**: `[x] Complete` (Commit `57dad5e`)
- **Description**: Executes Hybrid RRF queries fusing vector cosine similarity and full-text BM25 rankings over session transcripts.
- **Verification**: Unit test in `internal/mcpserver/tools_test.go::TestTool_SearchConversationHistory`.

### `MCP-04`: `get_messages_around_time` Tool
- **Category**: `MCP` (`PositiveFeature`)
- **Status**: `[x] Complete` (Commit `57dad5e`)
- **Description**: Retrieves context windows of verbatim messages and OCR annotations surrounding a target timestamp.
- **Verification**: Unit test in `internal/mcpserver/tools_test.go::TestTool_GetMessagesAroundTime`.

### `MCP-05`: `search_shared_media` Tool
- **Category**: `MCP` (`PositiveFeature`)
- **Status**: `[x] Complete` (Commit `57dad5e`)
- **Description**: Queries image attachments by OCR extracted text and visual scene descriptions.
- **Verification**: Unit test in `internal/mcpserver/tools_test.go::TestTool_SearchSharedMedia`.

### `MCP-06`: `manage_tracked_contact` Tool
- **Category**: `MCP` (`PositiveFeature`)
- **Status**: `[x] Complete` (Commit `57dad5e`)
- **Description**: Enables/disables thread tracking, initiates historical backfill passes, or forces immediate dossier reconciliation.
- **Verification**: Unit test in `internal/mcpserver/tools_test.go::TestTool_ManageTrackedContact`.

### `TRANS-01`: Health Check Probe
- **Category**: `TRANS` (`PositiveFeature`)
- **Status**: `[x] Complete` (Commit `57dad5e` / PR #41, commit `151d981`)
- **Description**: Exposes `/health` returning HTTP 200 with database connectivity status and version JSON.
- **Verification**: Unit test in `internal/web/server_test.go::TestHealthEndpoint_ReturnsHealthy`.

### `TRANS-02`: Streamable MCP & SSE Endpoints
- **Category**: `TRANS` (`PositiveFeature`)
- **Status**: `[x] Complete` (Commit `57dad5e`)
- **Description**: Exposes Streamable HTTP `/mcp` and Server-Sent Events `/sse` on port 8090.
- **Verification**: Unit test in `internal/web/server_test.go::TestMCPEndpoints_HTTPAndSSE`.

### `TRANS-03`: Briefing API & Outbound Webhook
- **Category**: `TRANS` (`PositiveFeature`)
- **Status**: `[x] Complete` (PR #6, #12 / Commit `7456168`)
- **Description**: Exposes `/api/v1/briefing/today` for Home Assistant REST sensors and dispatches POST to `WEBHOOK_URL` when dossiers update.
- **Verification**: Unit test in `internal/web/webhook_test.go::TestWebhookDispatch_OnDossierUpdate`.

### `TRANS-04`: Configurable Diagnostics & Apprise/ntfy Alerts
- **Category**: `TRANS` (`PositiveFeature`)
- **Status**: `[x] Complete` (PR #35, commit `796debd`)
- **Description**: Monitors `libgm` connection events (`PhoneNotResponding`, `PhoneRespondingAgain`, `ListenFatalError`). To avoid alert fatigue from transient workplace cell dropouts, notification dispatch via Apprise/ntfy is optional (`NOTIFY_ON_PHONE_DISCONNECT=true`, default `false`) and requires silence exceeding a configurable grace period (default 15 minutes) before triggering an alert.
- **Verification**: Unit tests verifying debounced notification suppression for brief network reconnects.

### `INT-01`: Home Assistant MQTT Bi-Directional Bridge
- **Category**: `INT` (`PositiveFeature`)
- **Status**: `[ ] Deferred / Held` (Issue #32)
- **Description**: Supplements outbound HTTP webhooks with an optional MQTT client bridge supporting Home Assistant MQTT Discovery. Publishes sensor states (connection status, unread message count) and subscribes to a command topic allowing Home Assistant automations to send outgoing SMS/RCS messages.
- **Note**: Held / deferred in favor of universal Apprise/ntfy push alerting and Prometheus metrics.
- **Verification**: In-memory MQTT client tests verifying discovery payload publication and inbound message dispatch.
