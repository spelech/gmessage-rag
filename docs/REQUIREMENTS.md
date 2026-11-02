# Software Requirements Specification: gmessages-rag

This specification defines the functional requirements, category taxonomy, and automated test traceability matrix for **`gmessages-rag`** (Real-Time Google Messages RCS/SMS Ingestion, Vision OCR, Living Dossier & Hybrid RAG MCP Server).

---

## Requirement Category Taxonomy

Requirement identifiers use `<CATEGORY>-<NN>` format (`AUTH-01`, `DB-01`, `GUARD-01`, `MCP-01`, `SEC-01`, `TRANS-01`, `PIPE-01`). Do not use generic `REQ-` prefixes.

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

---

## Requirements and Traceability Matrix

| Requirement ID | Category | Type | Summary | Acceptance Criteria | Automated Test Proof |
| :--- | :--- | :--- | :--- | :--- | :--- |
| **`AUTH-01`** | `AUTH` | `PositiveFeature` | **Session Token Persistence** | Atomically writes `*libgm.AuthData` to `/data/auth/session.json` with `0600` permissions on `PairSuccessful` and `AuthTokenRefreshed`. | `internal/gm/pairing_test.go::TestSessionPersistence_Permissions` |
| **`AUTH-02`** | `AUTH` | `PositiveFeature` | **Dual Pairing Workflow** | Supports terminal QR printing (`gmessages-rag pair`) and web UI QR rendering (`GET /pair`) with live refresh. | `internal/gm/pairing_test.go::TestPairingHandlers` |\n| **`AUTH-03`** | `AUTH` | `PositiveFeature` | **Push Alerting & Remote Re-Auth** | Dispatches Apprise/ntfy push notifications when token refresh fails or session is revoked, with direct link to `/pair` web UI for mobile re-pairing. | `internal/notify/client_test.go::TestPushAlerting_ReAuthNotification` |
| **`AUTH-04`** | `AUTH` | `PositiveFeature` | **Single-Device Gaia Pairing (No-QR Flow)** | Supports Google Account (Gaia) pairing via `DoGaiaPairing`: displays matching emoji challenge on web UI (`GET /pair/gaia`) or sends via Apprise push for single-device pairing without camera QR scanning. | `internal/notify/client_test.go::TestPushAlerting_GaiaChallengeAlert` |\n| **`DB-01`** | `DB` | `PositiveFeature` | **Schema Migration & Vector Init** | Automatically applies embedded `001_init.sql` creating `conversations`, `conversation_sessions` (with `halfvec(1536)` HNSW), `messages`, `media_attachments`, and `contact_dossiers`. | `internal/db/db_test.go::TestDatabaseMigrations` |\n| **`DB-02`** | `DB` | `PositiveFeature` | **Message & Reaction Ingestion** | Normalizes microsecond/millisecond timestamps to UTC, resolves incoming/outgoing direction, and idempotently upserts messages and emoji reactions. | `internal/db/messages_test.go::TestUpsertMessages_Idempotent` |\n| **`DB-03`** | `DB` | `PositiveFeature` | **2-Hour Session Windowing** | Groups messages separated by $\le 2\text{ hours}$ into `conversation_sessions` with absolute calendar headers and inline media OCR annotations. | `internal/db/sessions_test.go::TestSessionGrouping_2HourGap` |\n| **`DB-04`** | `DB` | `PositiveFeature` | **Hybrid RRF Search Query** | Combines full-text BM25 (`ts_rank_cd`) and vector cosine similarity (`<=>`) using Reciprocal Rank Fusion ($k=60$) over session transcripts. | `internal/db/sessions_test.go::TestSearchSessionsHybrid_RRF` |\n| **`DB-05`** | `DB` | `PositiveFeature` | **Living Dossier State & Audit History** | Upserts current structured state in `contact_dossiers` and snapshots prior state to `contact_dossier_history` for rollback safety. | `internal/db/dossiers_test.go::TestDossierVersionAuditHistory` |\n| **`DB-06`** | `DB` | `PositiveFeature` | **Inferred Read/Delivery Heuristics** | When enabled (`ENABLE_READ_RECEIPT_HEURISTIC=true`), infers prior outgoing messages up to incoming reply timestamp are delivered and acknowledged for SMS/iPhone contacts lacking native receipts. | `internal/db/messages_test.go::TestUpsertMessages_ReadReceiptHeuristic` |\n| **`SEC-01`** | `SEC` | `FailClosedGuardrail` | **Content-Addressed Media Decryption** | Decrypts RCS attachments, computes SHA-256 hash, and stores files at `/data/media/<h1h2>/<h3h4>/<hash>.<ext>` with duplicate avoidance. | `internal/gm/media_test.go::TestMediaDownload_SHA256Pathing` |\n| **`SEC-02`** | `SEC` | `PositiveFeature` | **Full-Resolution Media Retrieval** | When opted-in (`PULL_FULL_SIZE_MEDIA=true`), requests full-resolution image assets via `GetFullSizeImage` before running Vision OCR (default: progressive thumbnail). | `internal/gm/media_test.go::TestMediaDownload_FullSizeOption` |\n| **`GUARD-01`** | `GUARD` | `FailClosedGuardrail` | **Prompt Injection XML Boundary** | All raw message texts, transcripts, and OCR text returned by MCP tools are wrapped in `<untrusted_conversation_data>` tags. | `internal/mcpserver/guardrails_test.go::TestWrapUntrustedData_Escaping` |\n| **`GUARD-02`** | `GUARD` | `FailClosedGuardrail` | **Send-Message Restriction** | Deliberately omits outbound `send_message` MCP tool unless `ENABLE_MCP_SEND_MESSAGE=true` is explicitly configured. | `internal/mcpserver/tools_test.go::TestSendMessageTool_RegistrationGuard` |\n| **`PIPE-01`** | `PIPE` | `PositiveFeature` | **Vision OCR Sidecar** | Extracts verbatim OCR text and 1-2 sentence visual description for image attachments in tracked conversations via OpenAI-compatible vision model. | `internal/pipeline/vision_test.go::TestVisionOCR_Extraction` |\n| **`PIPE-02`** | `PIPE` | `PositiveFeature` | **Per-Conversation Idle Debouncer** | Maintains thread-safe timers per tracked conversation; triggers pipeline run after 30 minutes of messaging silence. | `internal/pipeline/debouncer_test.go::TestDebouncer_ResetOnNewMessage` |\n| **`PIPE-03`** | `PIPE` | `PositiveFeature` | **LLM State Reconciliation Engine** | Reconciles new sessions against current dossier: anchors relative dates, applies chronological overrides, transitions past events, and exports Markdown to disk. | `internal/pipeline/dossier_test.go::TestDossierReconciliation_DateAnchoring` |\n| **`PIPE-04`** | `PIPE` | `PositiveFeature` | **Google Contacts Enrichment** | Pulls `ListContacts` / `ListTopContacts` to enrich dossiers with contact names, avatars, phone labels, and relationship metadata. | `internal/pipeline/contacts_test.go::TestContactsSync_DossierEnrichment` |\n| **`PIPE-05`** | `PIPE` | `PositiveFeature` | **Action-Aware Smart Debouncing** | Listens to participant typing events (`UpdateEvents_TypingEvent`) and outbound drafts to reset or pause the 30-minute idle window dynamically. | `internal/pipeline/debouncer_test.go::TestDebouncer_TypingIndicatorReset` |\n| **`PIPE-06`** | `PIPE` | `PositiveFeature` | **Standardized Batch ETL Ingestion** | Staged pipeline for long conversation backfills: chunked cursor fetching, normalized transformation, and batched pgvector staging with bounded memory usage. | `internal/pipeline/etl_test.go::TestETLPipeline_BatchProcessing` |\n| **`MCP-00`** | `MCP` | `PositiveFeature` | **2026-07-28 Spec & Ping Support** | Strictly adheres to the 2026-07-28 MCP specification: returns `Mcp-Protocol-Version: 2026-07-28` header, handles `ping` JSON-RPC method returning empty object `{}`, supports `initialize`/`notifications/initialized`, and runs over Streamable HTTP (`POST /mcp`), SSE (`GET /sse`), and stdio. | `internal/mcpserver/server_test.go::TestProtocol_20260728_PingAndHandshake` |\n| **`MCP-01`** | `MCP` | `PositiveFeature` | **`list_conversations` Tool** | Lists conversations with IDs, contact names, participant identifiers, tracked status, and last activity timestamp. | `internal/mcpserver/tools_test.go::TestTool_ListConversations` |\n| **`MCP-02`** | `MCP` | `PositiveFeature` | **`get_contact_dossier` Tool** | Returns current Markdown dossier, structured JSON state, plus live tail of unindexed messages sent after last dossier reconciliation. | `internal/mcpserver/tools_test.go::TestTool_GetContactDossier` |\n| **`MCP-03`** | `MCP` | `PositiveFeature` | **`search_conversation_history` Tool** | Executes Hybrid RRF query across sessions with optional date bounds and returns formatted transcripts. | `internal/mcpserver/tools_test.go::TestTool_SearchConversationHistory` |\n| **`MCP-04`** | `MCP` | `PositiveFeature` | **`get_messages_around_time` Tool** | Pulls verbatim message context and media OCR annotations within $\pm \text{window\_minutes}$ of a target timestamp. | `internal/mcpserver/tools_test.go::TestTool_GetMessagesAroundTime` |\n| **`MCP-05`** | `MCP` | `PositiveFeature` | **`search_shared_media` Tool** | Searches media attachments by OCR text and visual descriptions. | `internal/mcpserver/tools_test.go::TestTool_SearchSharedMedia` |\n| **`MCP-06`** | `MCP` | `PositiveFeature` | **`manage_tracked_contact` Tool** | Enables/disables thread tracking, initiates historical pagination backfill, or forces immediate dossier refresh. | `internal/mcpserver/tools_test.go::TestTool_ManageTrackedContact` |\n| **`TRANS-01`** | `TRANS` | `PositiveFeature` | **Health Check Probe** | Exposes `/health` returning HTTP 200 with database connectivity status and version JSON. | `internal/web/server_test.go::TestHealthEndpoint_ReturnsHealthy` |\n| **`TRANS-02`** | `TRANS` | `PositiveFeature` | **Streamable MCP & SSE Endpoints** | Exposes Streamable HTTP `/mcp` and Server-Sent Events `/sse` on port 8090. | `internal/web/server_test.go::TestMCPEndpoints_HTTPAndSSE` |\n| **`TRANS-03`** | `TRANS` | `PositiveFeature` | **Briefing API & Outbound Webhook** | Exposes `/api/v1/briefing/today` for Home Assistant REST sensors and dispatches POST to `WEBHOOK_URL` when dossiers update. | `internal/web/webhook_test.go::TestWebhookDispatch_OnDossierUpdate` |\n| **`TRANS-04`** | `TRANS` | `PositiveFeature` | **Configurable Diagnostics & Apprise/ntfy Alerts** | Monitors phone responsiveness (`PhoneNotResponding`), but alerts only if configured (`NOTIFY_ON_PHONE_DISCONNECT=true`) and debounced by $N$ minutes to prevent false alarms during brief workplace cell drops. | `internal/notify/watchdog_test.go::TestWatchdog_PhoneDisconnect_GracePeriodTrigger` |
| **`UI-01`** | `UI` | `PositiveFeature` | **Web Management Dashboard** | Embedded SPA providing live daemon status, database connectivity metrics, tracked contact toggle table, and pairing QR visualizer. | `internal/web/ui_test.go::TestUI_DashboardRendering` |
| **`UI-02`** | `UI` | `PositiveFeature` | **Interactive Test Bench (Context-Cortex style)** | Developer harness in web UI to execute ad-hoc Hybrid RRF queries, test MCP tools with live responses, inspect XML boundaries, and review dossier history diffs. | `internal/web/testbench_test.go::TestUI_TestBenchExecution` |
| **`UI-03`** | `UI` | `PositiveFeature` | **Parameter & Pipeline Tuner** | Web interface to view and hot-tune pipeline parameters (debouncer idle duration, RRF $k$ factor, OCR model endpoint, backfill window). | `internal/config/tuner_test.go::TestRuntimeTuner_ConcurrentUpdatesAndNotification` |

---

## Detailed Requirement Definitions

### `AUTH-01`: Session Token Persistence
- **Category**: `AUTH` (`PositiveFeature`)
- **Description**: Atomically write updated session credentials to `/data/auth/session.json.tmp` with POSIX file mode `0600` and rename to `/data/auth/session.json` upon `*events.PairSuccessful` and `*events.AuthTokenRefreshed`.
- **Verification**: Verify file mode is strictly `0600` and contents contain valid token state.

### `GUARD-01`: Prompt Injection XML Boundary
- **Category**: `GUARD` (`FailClosedGuardrail`)
- **Description**: To prevent indirect prompt injection from SMS/RCS contents, wrap all external conversational text in `<untrusted_conversation_data warning="...">` XML tags before presenting it to agent LLMs.
- **Verification**: Unit tests verifying that all MCP tool responses wrap text, transcript, and OCR outputs in the specified XML tags with proper escaping.

### `DB-04`: Hybrid RRF Search Query
- **Category**: `DB` (`PositiveFeature`)
- **Description**: Implement a PostgreSQL Common Table Expression (CTE) fusing full-text search (`search_tsv @@ websearch_to_tsquery('english', $2)`) and vector cosine similarity (`embedding <=> $5::halfvec`) via Reciprocal Rank Fusion ($k=60$).
- **Verification**: Query execution tests asserting correct RRF scoring and rank fusion ordering.

### `MCP-00`: 2026-07-28 Model Context Protocol Compliance & Ping Method
- **Category**: `MCP` (`PositiveFeature`)
- **Description**: The MCP server strictly implements the `2026-07-28` Model Context Protocol specification:
  1. **Handshake & Version**: Handles `initialize` method returning `protocolVersion: "2026-07-28"`, capabilities, and server info. Handles `notifications/initialized`.
  2. **Ping Support**: Handles `ping` JSON-RPC method returning empty object `{}` with the request ID.
  3. **Protocol Header**: HTTP responses (both Streamable HTTP and SSE) return `Mcp-Protocol-Version: 2026-07-28`.
  4. **Multi-Transport**: Supports stateless Streamable HTTP (`POST /mcp`), Server-Sent Events (`GET /sse`), and standard I/O (`stdio`).
- **Verification**: Unit tests in `internal/mcpserver/server_test.go` asserting `ping` handling, `Mcp-Protocol-Version: 2026-07-28` response header, initialization lifecycle, and tool invocation.

### `UI-01`: Web Management Dashboard
- **Category**: `UI` (`PositiveFeature`)
- **Description**: Embedded web UI served on `/` showing daemon status, libgm pairing state (with live QR code rendering), PostgreSQL connectivity and row counts, tracked contact toggles, and recent event logs.
- **Verification**: Browser and handler tests asserting HTML rendering, JSON status polling, and pairing widget interactivity.

### `UI-02`: Interactive Test Bench (Context-Cortex style)
- **Category**: `UI` (`PositiveFeature`)
- **Description**: Developer diagnostic test bench providing interactive sandboxes to:
  1. Test all 6 MCP tools with arbitrary arguments and inspect formatted output (including `<untrusted_conversation_data>` boundaries).
  2. Run ad-hoc Hybrid RRF queries against ingested conversation sessions with vector vs full-text score breakdown.
  3. Simulate Vision OCR extraction on user-uploaded test images.
  4. Inspect Living Dossier structured JSON, rendered Markdown, and audit snapshot history diffs.
- **Verification**: Integration tests asserting test bench API handlers (`/api/v1/testbench/*`) execute queries, return raw & wrapped payloads, and provide accurate diagnostic output.

### `UI-03`: Parameter & Pipeline Tuner
- **Category**: `UI` (`PositiveFeature`)
- **Description**: Web interface to inspect configuration and hot-tune operational parameters (such as debouncer idle window, RRF fusion $k$ factor, Vision OCR prompts, and backfill page limits) without restarting the container.
- **Verification**: Unit tests asserting parameter update endpoints persist runtime overrides and notify active workers.

### `AUTH-03`: Push Alerting & Remote Re-Auth
- **Category**: `AUTH` (`PositiveFeature`)
- **Description**: When Google session tokens fail to refresh, browser pairing is revoked, or authentication unlinks, the daemon sends an alert via Apprise/ntfy with a direct link (or embedded action) to `/pair` on the Web Management UI, allowing users to approve/scan pairing directly from their mobile phone or remote browser without container terminal access.
- **Verification**: Unit tests verifying alert dispatch payload construction and URL endpoint formatting.

### `DB-06`: Inferred Read/Delivery Heuristics
- **Category**: `DB` (`PositiveFeature`)
- **Description**: For SMS or iPhone RCS threads where explicit delivery or read receipts are omitted by the carrier or client, when `ENABLE_READ_RECEIPT_HEURISTIC=true` is set, any incoming message from a contact automatically causes all preceding outgoing messages in that conversation up to the incoming message timestamp to be marked as delivered and acknowledged.
- **Verification**: Ingestion tests with simulated interleaved incoming and outgoing messages verifying status progression.

### `SEC-02`: Full-Resolution Media Retrieval
- **Category**: `SEC` (`PositiveFeature`)
- **Description**: When `PULL_FULL_SIZE_MEDIA=true` is enabled (default `false`), the media handler calls `libgm.Client.GetFullSizeImage` to download original high-resolution assets before invoking the Vision OCR pipeline, conserving bandwidth by default while providing crystal-clear OCR for users who require it.
- **Verification**: Attachment downloader tests asserting full-size fetch execution when flag is active vs thumbnail fallback.

### `PIPE-04`: Google Contacts Enrichment
- **Category**: `PIPE` (`PositiveFeature`)
- **Description**: Periodically calls `libgm.Client.ListContacts` and `ListTopContacts` to extract profile full names, contact avatars, relationship nicknames, and phone label types (mobile/work/home), enriching `contact_dossiers` with ground-truth contact information beyond RCS group subject strings.
- **Verification**: Integration test asserting contact metadata sync and dossier enrichment.

### `PIPE-05`: Action-Aware Smart Debouncing
- **Category**: `PIPE` (`PositiveFeature`)
- **Description**: Enhances the 30-minute idle debouncer by listening to participant typing indicators (`UpdateEvents_TypingEvent`) and outbound drafts/responses. If a participant starts typing or the user replies, the debouncer timer is automatically reset and held open to prevent premature session cutoffs mid-conversation.
- **Verification**: Timer lifecycle tests verifying reset on typing event injection.

### `PIPE-06`: Standardized Batch ETL Ingestion
- **Category**: `PIPE` (`PositiveFeature`)
- **Description**: Implements a standardized three-stage ETL pipeline for historical backfill and thread ingestion:
  1. **Extract**: Cursor-paginated batch retrieval from `libgm.FetchMessages` with configurable batch sizes (e.g., 100 messages/batch).
  2. **Transform**: Timestamp normalization across magnitudes, direction resolution, read-receipt heuristics, and SHA-256 media deduction.
  3. **Load**: Transactional batch upsert into `messages` and atomic progression of `backfill_cursor`, avoiding memory bloat or table lock contention on threads exceeding 10,000 messages.
- **Verification**: Pipeline test verifying memory boundedness and cursor resumption across large simulated backfill datasets.

### `TRANS-04`: Configurable Diagnostics & Apprise/ntfy Alerts
- **Category**: `TRANS` (`PositiveFeature`)
- **Description**: Monitors `libgm` connection events (`PhoneNotResponding`, `PhoneRespondingAgain`, `ListenFatalError`). To avoid alert fatigue from transient workplace cell dropouts, notification dispatch via Apprise/ntfy is optional (`NOTIFY_ON_PHONE_DISCONNECT=true`, default `false`) and requires silence exceeding a configurable grace period (default 15 minutes) before triggering an alert.
- **Verification**: Unit tests verifying debounced notification suppression for brief network reconnects.

### `AUTH-04`: Single-Device Gaia Pairing (No-QR Flow)
- **Category**: `AUTH` (`PositiveFeature`)
- **Description**: Solves the single-device pairing dilemma when no second screen is available. Instead of scanning a camera QR code, `gmessages-rag` leverages Google Messages' native Google Account (Gaia) pairing protocol:
  1. The daemon triggers `libgm.Client.DoGaiaPairing` (or `StartGaiaPairing`), which requests an interactive challenge from Google.
  2. Google Messages generates a pairing emoji challenge (e.g. 🐶 / 🚀 / ☕).
  3. The daemon renders this emoji challenge on `EXTERNAL_WEB_URL/pair/gaia` or pushes the emoji name directly to the user's phone via an Apprise/ntfy push notification.
  4. The user taps the notification on their phone, opens Google Messages, and simply taps the matching emoji on their screen to grant instant pairing approval without using a camera.
- **Verification**: Unit tests in `internal/gm/gaia_pairing_test.go` asserting emoji callback invocation, Ukey2 handshake processing, and `PairSuccessful` emission upon challenge confirmation.

### `HARN-01`: Controls-Grade Protobuf Disturbance Simulation Fixture
- **Category**: `HARN` (`PositiveFeature`)
- **Description**: Provides an in-memory Google Messages WebSocket simulation harness capable of injecting raw protobuf disturbance frames (pairing challenges, sudden socket disconnections, bursts of concurrent RCS media attachments, rapid-fire typing indicators, corrupt chunks) to verify daemon resilience without physical hardware.
- **Verification**: Simulation suite running in CI with zero hardware dependencies, verifying deterministic recovery.

### `SEC-03`: CI Vulnerability Scanning & Concurrency Race Detection
- **Category**: `SEC` (`NonFunctional`)
- **Description**: Enforces automated security auditing and concurrency verification in CI:
  1. `govulncheck`: Static call-graph vulnerability scanning against the Go Vulnerability Database.
  2. `go test -race`: Concurrency race detection across all internal packages to guarantee data-race-free timer, channel, and connection operations.
- **Verification**: Mandatory CI gate steps failing on any detected call-graph CVE or data race.

### `OBS-01`: Standardized Metrics & Observability Endpoints
- **Category**: `OBS` (`PositiveFeature`)
- **Description**: Exposes runtime Prometheus metrics (`/metrics`) and health probes (`/healthz`):
  - Ingestion counters (`gmessages_messages_ingested_total`, `gmessages_media_bytes_decrypted_total`).
  - Pipeline gauges (`gmessages_active_websocket_connections`, `gmessages_session_chunks_created_total`).
  - Lightweight `/healthz` endpoint checking database pool reachability and WebSocket listener status for container orchestrators.
- **Verification**: HTTP handler tests verifying metric emission and health status checks.

### `INT-01`: Home Assistant MQTT Bi-Directional Bridge
- **Category**: `INT` (`PositiveFeature`)
- **Description**: Supplements outbound HTTP webhooks with an optional MQTT client bridge supporting Home Assistant MQTT Discovery. Publishes sensor states (connection status, unread message count) and subscribes to a command topic allowing Home Assistant automations to send outgoing SMS/RCS messages.
- **Verification**: In-memory MQTT client tests verifying discovery payload publication and inbound message dispatch.





