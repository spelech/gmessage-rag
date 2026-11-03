# Software Requirements Specification: gmessages-rag

This specification defines the functional requirements, category taxonomy, and automated test traceability matrix for **`gmessages-rag`** (Real-Time Google Messages RCS/SMS Ingestion, Vision OCR, Living Dossier & Hybrid RAG MCP Server).

---

## Requirement Category Taxonomy

Requirement identifiers use `<CATEGORY>-<NN>` format (`AUTH-01`, `DB-01`, `GUARD-01`, `MCP-01`, `SEC-01`, `TRANS-01`, `PIPE-01`, `UI-01`, `NOTIF-01`, `OBS-01`, `HARN-01`, `CLEANUP-01`, `DOCKER-01`, `DAEMON-01`, `REFACTOR-01`). Do not use generic `REQ-` prefixes.

| Category Code | Domain Scope | Proof Types |
| :--- | :--- | :--- |
| **`AUTH`** | Google Messages cookie import & session token lifecycle | `PositiveFeature` / `FailClosedGuardrail` |
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

> **Status vocabulary (added 2026-11-02, Issue #75 reconciliation):**
> - `[x] Complete` — implemented and covered by a test that exists in the repository.
> - `[~] Partial` — a component exists and is tested, but part of the described behavior is not wired or not implemented.
> - `[ ] In Progress` — active work on a tracked issue.
> - `[ ] Not Started` — the described behavior has no implementation in the repository.
> - `[ ] Deferred` — intentionally postponed.

> **Traceability audit notice:** A code audit on 2026-11-02 found the SRS had marked several
> unbuilt behaviors as `[x] Complete`, referencing test paths that do not exist. Those rows are
> corrected below. CI now enforces this (`scripts/check_traceability.sh`). Do not mark a row
> `[x] Complete` unless its proof path exists and passes.

| Requirement ID | Category | Status | Summary | Merged PR / Commit | Acceptance Criteria | Automated Test Proof |
| :--- | :--- | :---: | :--- | :--- | :--- | :--- |
| **`AUTH-01`** | `AUTH` | [x] Complete | **Session Token Persistence** | Commit `7456168` (PR #7) | Atomically writes `*libgm.AuthData` to `/data/auth/session.json` with `0600` permissions on `PairSuccessful` and `AuthTokenRefreshed`. | `internal/gm/pairing_test.go::TestSessionPersistence_Permissions` |
| **`AUTH-02`** | `AUTH` | [x] Complete | **Cookie Import Pairing** | PR #99 | Supports cookie import from a cURL command, JSON map/array, raw `Cookie:` header, or semicolon pairs via CLI (`gmessages-rag pair --cookies`) and web dashboard (`GET/POST /pair`). Validates the required Google cookies and persists the session. | `internal/gm/cookies_test.go::TestParseCookies`, `internal/web/pair_test.go::TestCookiePairHandler` |
| **`AUTH-03`** | `AUTH` | [x] Complete | **Push Alerting & Remote Re-Auth** | Commit `7456168` (PR #35) | Dispatches Apprise/ntfy push notifications when token refresh fails or session is revoked, with direct link to `/pair` web UI for mobile re-pairing. | `internal/notify/client_test.go::TestPushAlerting_ReAuthNotification` |
| **`AUTH-04`** | `AUTH` | [~] Partial | **Single-Device Gaia Pairing after Cookie Import** | PR #36, commit `fd1f7a3` | After cookie import validation, supports Google Account (Gaia) pairing via `PairGaia`, displaying a matching emoji challenge. **Partial:** the pairing code and its callback/cancellation paths are implemented and tested hermetically; the end-to-end success path requires the gated live smoke test (`HARN-04`). | `internal/gm/gaia_test.go::TestGaiaPairing_CallbackInvocation` |
| **`DB-01`** | `DB` | [x] Complete | **Schema Migration & Vector Init** | PR #4, #5, #7, #13, #15, #16 | Automatically applies embedded `001_init.sql` creating `conversations`, `conversation_sessions` (with `halfvec(1536)` HNSW), `messages`, `media_attachments`, and `contact_dossiers`. | `internal/db/db_test.go::TestGetMigrationScripts` |
| **`DB-02`** | `DB` | [x] Complete | **Message & Reaction Ingestion** | PR #4, #5, #7, #13, #15, #16 | Normalizes microsecond/millisecond timestamps to UTC, resolves incoming/outgoing direction, and idempotently upserts messages and emoji reactions. | `internal/db/messages_test.go::TestUpsertMessages_Idempotent` |
| **`DB-03`** | `DB` | [x] Complete | **2-Hour Session Windowing** | PR #4, #5, #7, #13, #15, #16 | Groups messages separated by $\le 2\text{ hours}$ into `conversation_sessions` with absolute calendar headers and inline media OCR annotations. | `internal/db/sessions_test.go::TestSessionGrouping_2HourGap` |
| **`DB-04`** | `DB` | [x] Complete | **Hybrid RRF Search Query** | PR #4, #5, #7, #13, #15, #16 | Combines full-text BM25 (`ts_rank_cd`) and vector cosine similarity (`<=>`) using Reciprocal Rank Fusion ($k=60$) over session transcripts. | `internal/db/sessions_test.go::TestSearchSessionsHybrid_RRF` |
| **`DB-05`** | `DB` | [x] Complete | **Living Dossier State & Audit History** | PR #4, #5, #7, #13, #15, #16 | Upserts current structured state in `contact_dossiers` and snapshots prior state to `contact_dossier_history` for rollback safety. | `internal/db/dossiers_test.go::TestDossierVersionAuditHistory` |
| **`DB-06`** | `DB` | [x] Complete | **Inferred Read/Delivery Heuristics** | PR #37, commit `0c88b51` | When enabled (`ENABLE_READ_RECEIPT_HEURISTIC=true`), infers prior outgoing messages up to incoming reply timestamp are delivered and acknowledged for SMS/iPhone contacts lacking native receipts. | `internal/db/receipts_test.go::TestInferDeliveryReceipts` |
| **`DB-07`** | `DB` | [x] Complete | **Postgres-Backed CI Integration Tests** | Issue #72, PR #73, commit `2a37861` | CI provisions a `pgvector/pgvector:pg17` service with `DATABASE_URL`, so `internal/db`, `internal/pipeline`, and `internal/gm` integration tests execute on every push. Enforces a ≥ 75% coverage floor. | `gate:.github/workflows/ci.yml` |
| **`GUARD-01`** | `GUARD` | [x] Complete | **Prompt Injection XML Boundary** | Commit `57dad5e` | All raw message texts, transcripts, and OCR text returned by MCP tools are wrapped in `<untrusted_conversation_data>` tags. | `internal/mcpserver/guardrails_test.go::TestWrapUntrustedData_Escaping` |
| **`GUARD-02`** | `GUARD` | [x] Complete | **Send-Message Restriction** | Commit `57dad5e` | Deliberately omits outbound `send_message` MCP tool unless `ENABLE_MCP_SEND_MESSAGE=true` is explicitly configured. | `internal/mcpserver/tools_test.go::TestSendMessageTool_RegistrationGuard` |
| **`PIPE-01`** | `PIPE` | [ ] Not Started | **Vision OCR Sidecar** | — | Extracts verbatim OCR text and a 1-2 sentence visual description for image attachments via an OpenAI-compatible vision model. **Not implemented:** no vision client exists; `VISION_MODEL`/`LLM_BASE_URL` are unused. | None |
| **`PIPE-02`** | `PIPE` | [x] Complete | **Per-Conversation Idle Debouncer** | PR #6, #12; Issue #81 | Maintains thread-safe timers per tracked conversation; triggers a pipeline run after 30 minutes of silence. The daemon's `OnFlush` callback runs the full pipeline: Vision OCR, 2-hour sessionization, and Living Dossier reconciliation. | `internal/pipeline/debouncer_test.go::TestDebouncer_ResetOnNewMessage` |
| **`PIPE-03`** | `PIPE` | [x] Complete | **LLM State Reconciliation Engine** | Issue #78 | Reconciles new sessions against the current dossier: anchors relative dates, applies chronological overrides, transitions past events, and exports Markdown. `internal/pipeline/dossier.go` calls `DOSSIER_MODEL` through an injectable OpenAI-compatible client, persists via `db.UpsertContactDossier`, and spools `<contact>.md` under `DOSSIER_DIR`. | `internal/pipeline/dossier_test.go::TestDossierReconciliation_DateAnchoring` |
| **`PIPE-04`** | `PIPE` | [x] Complete | **Google Contacts Enrichment** | Issue #78 (PR #47, commit `836834b`) | Pulls `ListContacts` / `ListTopContacts` and enriches dossiers with contact names, avatars, nicknames, and number labels. `internal/pipeline/contacts.go` merges the roster into the reconciled structured state and Markdown. | `internal/pipeline/contacts_test.go::TestContactsSync_DossierEnrichment` |
| **`PIPE-05`** | `PIPE` | [x] Complete | **Action-Aware Smart Debouncing** | PR #40, commit `09c2323`; Issue #81 | Resets or pauses the idle window on typing events and outbound drafts. The gm client forwards `*gmproto.TypingData` from libgm to `debouncer.RecordTypingType`, so participant typing holds the window open. | `internal/pipeline/debouncer_test.go::TestDebouncer_TypingExtendsWindow`, `internal/gm/client_test.go::TestClient_TypingEventDispatch` |
| **`PIPE-06`** | `PIPE` | [~] Partial | **Standardized Batch ETL Ingestion** | PR #48, commit `badf3e9` | Chunked cursor fetching and normalized transformation for backfills. **Partial:** message fetch/upsert ETL is implemented and tested; no embedding/vector staging occurs. | `internal/pipeline/etl_test.go::TestETLPipeline_BatchProcessing` |
| **`OBS-01`** | `OBS` | [x] Complete | **Prometheus Metrics & Health Probes** | PR #41, commit `151d981` | Exposes standard Prometheus metrics (`/metrics`) and health check probes (`/healthz`) with database pool reachability and WebSocket listener status. | `internal/obs/metrics_test.go::TestMetrics_HTTPEndpointScrape` |
| **`NOTIF-01`** | `NOTIF` | [x] Complete | **Apprise & ntfy Push Alerting** | PR #35, commit `796debd` | Dispatches high-priority push notifications via Apprise and ntfy endpoints with embedded action links for mobile re-auth and connection events. | `internal/notify/client_test.go::TestPushAlerting_ReAuthNotification` |
| **`NOTIF-02`** | `NOTIF` | [x] Complete | **Debounced Connection Watchdog** | PR #35, commit `796debd` | Monitors `PhoneNotResponding` and suppresses alerts during configurable grace periods (default 15m) to eliminate transient cell drop fatigue. | `internal/notify/watchdog_test.go::TestWatchdog_PhoneDisconnect_GracePeriodTrigger` |
| **`SEC-01`** | `SEC` | [x] Complete | **Content-Addressed Media Decryption** | PR #34, commit `1141504`; Issue #81 | Decrypts RCS attachments, computes a SHA-256 hash, and stores files at `/data/media/<h1h2>/<h3h4>/<hash>.<ext>` with duplicate avoidance. The ETL ingestion path calls `db.UpsertMediaAttachment` for each decrypted attachment, so stored images feed the Vision OCR service. | `internal/gm/media_test.go::TestMediaDecryption_AESGCM`, `internal/pipeline/media_test.go::TestMediaIngestor_PersistsDecryptedMedia` |
| **`SEC-02`** | `SEC` | [x] Complete | **Full-Resolution Media Retrieval** | PR #38, commit `8d5b7e4` | When opted-in (`PULL_FULL_SIZE_MEDIA=true`), requests full-resolution image assets via `GetFullSizeImage` before running Vision OCR (default: progressive thumbnail). | `internal/gm/media_test.go::TestMediaDownload_FullSizeOption` |
| **`SEC-03`** | `SEC` | [x] Complete | **CI Vulnerability Scanning & Race Checks** | Issue #30, commit `30b0cb6` | Enforces `govulncheck` static call-graph vulnerability scanning and `go test -race` concurrency validation in CI and Makefile targets. | `gate:Makefile`, `gate:.github/workflows/ci.yml` |
| **`HARN-01`** | `HARN` | [x] Complete | **Protobuf Disturbance Simulation Fixture** | Issue #29, commit `ecac8e2` | In-memory Google Messages simulation server injecting synthetic protobuf frames (disconnects, media bursts, typing events, payload corruption). `MockGMServer` now exposes an in-process `Settings()`/`Transport()` seam consumed by real `libgm`/`gm.Client` code, so injected disturbances surface through the production ingestion path. | `internal/gm/testing/mock_server_test.go::TestMockServer_DisturbanceFailsFetch`, `internal/gm/testing/mock_server_test.go::TestMockGMServer_Disturbance_HTTPErrorAndCorruptPayload`, `internal/gm/testing/mock_server_test.go::TestMockGMServer_LifecycleAndEmission` |
| **`HARN-02`** | `HARN` | [x] Complete | **In-Process libgm Protocol Emulator** | Issue #62, PR #69, commit `132d12e` | Hermetic, in-process implementation of the Google Messages RPC surface, wired through `exhttp.ClientSettings.TransportOverride` (no libgm fork). Covers the pairing handshake (`RegisterPhoneRelay`, `/web/config`, long-poll `PairEvent`), `FetchMessages` cursor paging, `ListContacts`, and media download (`/upload`). | `internal/gm/emulator_e2e_test.go::TestEmulator_PairingCompletesPairDone`, `internal/gm/testing/mock_server_test.go::TestMockServer_FetchMessages`, `internal/gm/testing/mock_server_test.go::TestMockServer_ListContacts` |
| **`HARN-03`** | `HARN` | [x] Complete | **Hermetic Test Suite & Live Network Quarantine** | Issue #63 | Default `go test ./...` runs fully offline: `internal/gm` installs a deny-all HTTP transport via the `libgm` constructor seam. Live-network tests are quarantined behind the `live` build tag and `LIVE_GM_TEST=1`. | `internal/gm/hermetic_test.go::TestHermeticGuard_BlocksOutboundRegistration` |
| **`HARN-04`** | `HARN` | [ ] In Progress | **Gated Real-Device Live Smoke Test** | Issue #65 | One opt-in end-to-end test (`//go:build live`) that imports cookies from `COOKIES`/`COOKIES_PATH` or uses a pre-paired session, approves the Gaia emoji challenge when needed, asserts `0600` session persistence, connects, waits for one real inbound message, and verifies the persistence handoff. Never runs in CI. | `internal/gm/live_smoke_test.go::TestLiveSmoke_EndToEnd` |
| **`UI-01`** | `UI` | [x] Complete | **Web Management Dashboard** | PR #46, commit `8dfb444` | Embedded SPA providing live daemon status, database connectivity metrics, tracked contact toggle table, and cookie-import pairing console. | `internal/web/dashboard_test.go::TestDashboardUI_AssetServing`, `internal/web/pair_test.go::TestCookiePairHandler`, `internal/web/api.go` handlers |
| **`UI-02`** | `UI` | [~] Partial | **Interactive Test Bench (Context-Cortex style)** | PR #45, commit `91e8fcc` | Developer harness in the web UI to run ad-hoc Hybrid RRF queries, test MCP tools, inspect XML boundaries, and review dossier history. **Partial:** the harness and its handlers are implemented and tested, but MCP tool calls return stub text and RRF runs against unpopulated sessions. | `internal/web/testbench_test.go::TestUI_TestBenchExecution` |
| **`UI-03`** | `UI` | [x] Complete | **Parameter & Pipeline Tuner** | PR #44, commit `2407ed6` | Web interface to view and hot-tune pipeline parameters (debouncer idle duration, RRF $k$ factor, OCR model endpoint, backfill window). | `internal/config/tuner_test.go::TestRuntimeTuner_ConcurrentUpdatesAndNotification` |
| **`UI-05`** | `UI` | [~] Partial | **Embedded Test Bench & Tuner in Dashboard** | PR #49 | Integrates UI-02 and UI-03 into tabbed navigation with interactive MCP tools, hybrid RRF search, guardrail sanitizer, and hot-tuning REST endpoints. **Partial:** panels and tuner work; embedded MCP tool calls resolve to stubs. | `internal/web/dashboard_test.go::TestDashboardUI_AssetServing` |
| **`UI-06`** | `UI` | [x] Complete | **VitePress Google Brand Theme** | Issue #71, PR #74, commit `f0458c0` | Custom VitePress theme (`docs/.vitepress/theme`) applies the Google four-color palette (blue #4285F4, green #34A853, yellow #FBBC05, red #EA4335) to brand/tip/warning/danger tokens with light and dark variants, a four-color hero wordmark, and a palette-aligned Mermaid theme. | `docs/.vitepress/theme/custom.css`, screenshots `assets/screenshots/docs_home_light.png` |
| **`CLEANUP-01`**| `CLEANUP` | [x] Complete | **Dead Code & Error Handling Hardening** | Issue #42, commit `c279ebd` | Eliminated ignored errors, enforced strict error propagation on file operations, and added `goleak` goroutine lifecycle validation. | `gate:Makefile` (`make test`), `go test -race ./...` |
| **`DOCKER-01`** | `DOCKER` | [x] Complete | **Hardened Non-Root Container & PUID/PGID** | Issue #42, commit `c279ebd` | Hardened multi-stage non-root container with `PUID`/`PGID` permission handling, `.env.example` alignment, and immutable volume mounts. | `Dockerfile`, `docker-compose.yml` |
| **`DAEMON-01`** | `DAEMON` | [x] Complete | **Unified End-to-End Daemon Orchestration** | Issue #42, commit `c279ebd` | Orchestrated root `ServeMux` linking `/`, `/api/*`, `/testbench`, `/mcp`, `/metrics`, and `/healthz` with graceful SIGINT/SIGTERM shutdown and background ETL sync. | `cmd/gmessages-rag/main_test.go::TestBuildServeMux_RouteRegistration` |
| **`REFACTOR-01`**| `REFACTOR`| [x] Complete | **Comprehensive Architecture Purge** | Issue #42, commit `c279ebd` | Holistic code smell elimination, clean package boundaries, and verification across all daemon subsystems. | `gate:Makefile` (`make lint`, `make test`) |
| **`MCP-00`** | `MCP` | [x] Complete | **2026-07-28 Spec & Ping Support** | Commit `57dad5e` | Strictly adheres to the 2026-07-28 MCP specification: returns `Mcp-Protocol-Version: 2026-07-28` header, handles `ping` JSON-RPC method returning empty object `{}`, supports `initialize`/`notifications/initialized`, and runs over Streamable HTTP (`POST /mcp`), SSE (`GET /sse`), and stdio. | `internal/mcpserver/server_test.go::TestProtocol_20260728_PingAndHandshake` |
| **`MCP-01`** | `MCP` | [x] Complete | **`list_conversations` Tool** | Issue #79 | Lists conversations with IDs, contact names, participant identifiers, tracked status, and last activity. Handler is a thin wrapper over `db.ListConversations`; no placeholder text. | `internal/mcpserver/tools_test.go::TestTools_ListConversations_RealDB` |
| **`MCP-02`** | `MCP` | [x] Complete | **`get_contact_dossier` Tool** | Issue #79 | Returns the current Markdown dossier, structured JSON state, and a live tail of unindexed messages. Handler wraps `db.GetContactDossier` and `db.GetDossierLiveTail`. | `internal/mcpserver/tools_test.go::TestTools_GetContactDossier_RealDB` |
| **`MCP-03`** | `MCP` | [x] Complete | **`search_conversation_history` Tool** | Issue #79 | Executes a Hybrid RRF query across sessions with optional date bounds and returns formatted transcripts. Handler wraps `db.SearchSessionsHybrid`. | `internal/mcpserver/tools_test.go::TestTools_SearchConversationHistory_RealDB` |
| **`MCP-04`** | `MCP` | [x] Complete | **`get_messages_around_time` Tool** | Issue #79 | Pulls verbatim message context and media OCR annotations within a time window. Handler wraps `db.GetMessagesAroundTime` and `db.GetMediaAttachmentsForMessage`. | `internal/mcpserver/tools_test.go::TestTools_GetMessagesAroundTime_RealDB` |
| **`MCP-05`** | `MCP` | [x] Complete | **`search_shared_media` Tool** | Issue #79 | Searches media attachments by OCR text and visual descriptions. Handler wraps `db.SearchMediaAttachments`. | `internal/mcpserver/tools_test.go::TestTools_SearchSharedMedia_RealDB` |
| **`MCP-06`** | `MCP` | [x] Complete | **`manage_tracked_contact` Tool** | Issue #79 | Enables or disables thread tracking, resets backfill state, or forces a dossier refresh. Handler wraps `db.SetConversationTracked`, `db.ResetConversationBackfill`, and `db.GetContactDossier`. | `internal/mcpserver/tools_test.go::TestTools_ManageTrackedContact_RealDB` |
| **`TRANS-01`** | `TRANS` | [x] Complete | **Health Check Probe** | Commit `57dad5e` / PR #41, commit `151d981` | Exposes `/health` and `/healthz` returning HTTP 200 with database connectivity status and version JSON. | `cmd/gmessages-rag/main_test.go::TestBuildServeMux_RouteRegistration`, `internal/obs/health_test.go::TestHealthHandler_Healthy` |
| **`TRANS-02`** | `TRANS` | [x] Complete | **Streamable MCP & SSE Endpoints** | Commit `57dad5e` | Exposes Streamable HTTP `/mcp` and Server-Sent Events `/sse` on port 8090. | `cmd/gmessages-rag/main_test.go::TestBuildServeMux_RouteRegistration`, `internal/mcpserver/server_test.go::TestProtocol_20260728_PingAndHandshake` |
| **`TRANS-03`** | `TRANS` | [x] Complete | **Briefing API & Outbound Webhook** | Issue #80 | Exposes `/api/v1/briefing` and `/api/v1/briefing/today` returning per tracked contact what changed since a caller-supplied `since` (RFC3339) or a default 24h lookback, sourced from `contact_dossier_history` with dossier-derived Markdown wrapped in untrusted-data boundaries. Dispatches a signed POST to `WEBHOOK_URL` (optional bearer/HMAC auth) with bounded backoff when the dossier reconciler updates a dossier. | `internal/web/briefing_test.go::TestBriefingAPI_ChangedSince`, `internal/notify/webhook_test.go::TestWebhookDispatch_RetriesOnFailure`, `cmd/gmessages-rag/main_test.go::TestBuildServeMux_RouteRegistration` |
| **`TRANS-04`** | `TRANS` | [x] Complete | **Configurable Diagnostics Alerts** | PR #35, commit `796debd` | Monitors phone responsiveness (`PhoneNotResponding`), but alerts only if configured (`NOTIFY_ON_PHONE_DISCONNECT=true`) and debounced by $N$ minutes. | `internal/notify/watchdog_test.go::TestWatchdog_PhoneDisconnect_GracePeriodTrigger` |
| **`INT-01`** | `INT` | [ ] Deferred | **Home Assistant MQTT Bridge** | Held (Issue #32) | Supplements outbound webhooks with an optional MQTT client bridge. **Note:** Deferred/held in favor of universal Apprise/ntfy push alerting and Prometheus metrics. | `internal/mqtt` baseline |

---

## Detailed Requirement Definitions

> **The traceability matrix above is authoritative.** Where a definition below carries an outdated
> `[x] Complete` status, the matrix and the `2026-11-02 Traceability Reconciliation` appendix govern.

### `AUTH-01`: Session Token Persistence
- **Category**: `AUTH` (`PositiveFeature`)
- **Status**: `[x] Complete` (Commit `7456168`, PR #7)
- **Description**: Atomically write updated session credentials to `/data/auth/session.json.tmp` with POSIX file mode `0600` and rename to `/data/auth/session.json` upon `*events.PairSuccessful` and `*events.AuthTokenRefreshed`.
- **Verification**: Verify file mode is strictly `0600` and contents contain valid token state.

### `AUTH-02`: Cookie Import Pairing
- **Category**: `AUTH` (`PositiveFeature`)
- **Status**: `[x] Complete` (PR #99)
- **Description**: Supports cookie import from a cURL command copied from browser DevTools, a flat or wrapped JSON object, a JSON array from a cookie export tool, a raw `Cookie:` header, or semicolon-separated pairs. The CLI (`gmessages-rag pair --cookies`) and the web dashboard (`GET/POST /pair`) accept the same formats. The importer validates the required Google cookies (`SID`, `HSID`, `OSID`, `SSID`, `APISID`, `SAPISID`), preserves extras, and never prints cookie values in errors.
- **Verification**: Parser tests in `internal/gm/cookies_test.go` and HTTP handler tests in `internal/web/pair_test.go` asserting form rendering, JSON status, and fail-closed rejection.

### `AUTH-03`: Push Alerting & Remote Re-Auth
- **Category**: `AUTH` (`PositiveFeature`)
- **Status**: `[x] Complete` (Commit `7456168`, PR #35)
- **Description**: When Google session tokens fail to refresh or authentication unlinks, the daemon sends an alert via Apprise/ntfy with a direct link (or embedded action) to `/pair` on the Web Management UI, allowing users to re-import cookies or approve the Gaia emoji challenge directly from their mobile phone or remote browser without container terminal access.
- **Verification**: Unit tests verifying alert dispatch payload construction and URL endpoint formatting.

### `AUTH-04`: Single-Device Gaia Pairing after Cookie Import
- **Category**: `AUTH` (`PositiveFeature`)
- **Status**: `[~] Partial` (PR #36, commit `fd1f7a3`)
- **Description**: After cookie import validation, `gmessages-rag` leverages Google Messages' native Google Account (Gaia) pairing protocol:
  1. The daemon triggers `libgm.Client.DoGaiaPairing` (or `StartGaiaPairing`), which requests an interactive challenge from Google.
  2. Google Messages generates a pairing emoji challenge (e.g. 🐶 / 🚀 / ☕).
  3. The daemon renders this emoji challenge on `EXTERNAL_WEB_URL/pair` or pushes the emoji name directly to the user's phone via an Apprise/ntfy push notification.
  4. The user opens Google Messages and taps the matching emoji to grant instant pairing approval without camera-based linking.
- **Verification**: Hermetic unit tests in `internal/gm/gaia_test.go` asserting emoji callback invocation and context cancellation. **The end-to-end success path (`PairSuccessful` after challenge confirmation) is only exercised by the gated live smoke test (`HARN-04`).**

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

### `DB-07`: Postgres-Backed CI Integration Tests
- **Category**: `DB` (`NonFunctional`)
- **Status**: `[ ] In Progress` (Issue #72)
- **Description**: CI provisions a `pgvector/pgvector:pg17` service exposing `DATABASE_URL`, so the DB integration suites (`internal/db`, `internal/pipeline`, `internal/gm`) execute on every push instead of skipping. A coverage-floor step fails the build if total coverage drops below 75% or `internal/db` drops below 75%.
- **Evidence (local measurement)**: Without a database, `internal/db` reports 7.3% and the suite 71.2%. With Postgres+pgvector, `internal/db` reports 80.5% and the suite 78.7%.
- **Verification**: `.github/workflows/ci.yml` Stage 2 `services.postgres` + `Enforce Coverage Floors`; local equivalent documented in `docs/operations/runbooks.md` and runnable via `make test-integration`.

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
- **Status**: `[x] Complete` (Issue #78)
- **Description**: Reconciles new sessions against current dossier: anchors relative dates, applies chronological overrides, transitions past events, and exports Markdown to disk. The reconciler (`internal/pipeline/dossier.go`) is injectable, calls the `DOSSIER_MODEL` chat endpoint through an OpenAI-compatible client, persists to `contact_dossiers` (snapshotting prior state to `contact_dossier_history`), and spools Markdown under `DOSSIER_DIR`.
- **Verification**: `internal/pipeline/dossier_test.go::TestDossierReconciliation_DateAnchoring` runs against real Postgres with a contract-stub LLM boundary, asserting the persisted dossier, audit history, and exported Markdown.

### `PIPE-04`: Google Contacts Enrichment
- **Category**: `PIPE` (`PositiveFeature`)
- **Status**: `[x] Complete` (Issue #78; fetch/parse PR #47, commit `836834b`)
- **Description**: Periodically calls `libgm.Client.ListContacts` and `ListTopContacts` to extract profile full names, contact avatars, relationship nicknames, and phone-number labels, enriching `contact_dossiers` with ground-truth contact information beyond RCS group subject strings. `internal/pipeline/contacts.go` merges the roster into the reconciled structured state and Markdown.
- **Verification**: `internal/pipeline/contacts_test.go::TestContactsSync_DossierEnrichment` asserts contact metadata sync and dossier enrichment against real Postgres.

### `PIPE-05`: Action-Aware Smart Debouncing
- **Category**: `PIPE` (`PositiveFeature`)
- **Status**: `[x] Complete` (PR #40, commit `09c2323`; Issue #81)
- **Description**: Enhances the 30-minute idle debouncer by listening to participant typing indicators (`UpdateEvents_TypingEvent`) and outbound drafts/responses. If a participant starts typing or the user replies, the debouncer timer is automatically reset and held open to prevent premature session cutoffs mid-conversation. The Google Messages client forwards each `*gmproto.TypingData` event to `SmartDebouncer.RecordTypingType` through the `OnTypingActivity` callback.
- **Verification**: `internal/pipeline/debouncer_test.go::TestDebouncer_TypingExtendsWindow` proves the typing event extends the idle window; `internal/gm/client_test.go::TestClient_TypingEventDispatch` proves the client forwards typing events verbatim.

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
- **Description**: Multi-channel push notification dispatcher supporting both self-hosted ntfy servers and universal Apprise gateways. Emits high-priority mobile alerts with action buttons for cookie/Gaia re-authentication and pairing approvals.
- **Verification**: Mock webhook dispatch tests verifying alert payloads and header tags.

### `NOTIF-02`: Debounced Connection Watchdog
- **Category**: `NOTIF` (`PositiveFeature`)
- **Status**: `[x] Complete` (PR #35, commit `796debd`)
- **Description**: Monitors `libgm` connection state transitions (`PhoneNotResponding`, `PhoneRespondingAgain`, `ListenFatalError`). Suppresses disconnect notifications during a configurable grace period (default 15m) to prevent alert fatigue during workplace cellular dips.
- **Verification**: Watchdog state tests validating alert suppression and reconnection recovery.

### `SEC-01`: Content-Addressed Media Decryption
- **Category**: `SEC` (`FailClosedGuardrail`)
- **Status**: `[x] Complete` (PR #34, commit `1141504`; Issue #81)
- **Description**: Decrypts RCS attachments, computes SHA-256 hash, and stores files at `/data/media/<h1h2>/<h3h4>/<hash>.<ext>` with duplicate avoidance. The ETL ingestion path persists each decrypted attachment through `db.UpsertMediaAttachment` (via `internal/pipeline/media.go`), so stored images are picked up by the Vision OCR service.
- **Verification**: `internal/gm/media_test.go::TestMediaDecryption_AESGCM` asserts the decrypted plaintext lands at the content-addressed SHA-256 path; `internal/pipeline/media_test.go::TestMediaIngestor_PersistsDecryptedMedia` asserts the ingestion caller records an unprocessed `media_attachments` row.

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
- **Status**: `[x] Complete` (Issue #29, commit `ecac8e2`; Issue #82)
- **Description**: Provides an in-memory Google Messages simulation harness capable of injecting raw protobuf disturbance frames (pairing challenges, sudden socket disconnections, bursts of concurrent RCS media attachments, rapid-fire typing indicators, corrupt chunks) to verify daemon resilience without physical hardware. `MockGMServer` additionally exposes an in-process `Settings()`/`Transport()` seam that the real `gm.Client` consumes through `exhttp.ClientSettings.TransportOverride`, so disturbance injection is exercised against production libgm code, not only the fixture's own unit tests.
- **Verification**: `internal/gm/testing/mock_server_test.go::TestMockServer_DisturbanceFailsFetch` drives the real `gm.Client.FetchMessages` and asserts the injected 503 surfaces through the ingestion path. `TestMockGMServer_Disturbance_HTTPErrorAndCorruptPayload` and `TestMockGMServer_LifecycleAndEmission` cover the SSE disturbance and framing surface. The suite runs in CI with zero hardware dependencies.

### `HARN-02`: In-Process libgm Protocol Emulator
- **Category**: `HARN` (`PositiveFeature`)
- **Status**: `[x] Complete` (Issue #62, PR #69, commit `132d12e`; Issue #82)
- **Description**: An in-repo, hermetic emulator for the Google Messages HTTP RPC surface that `libgm` speaks, wired through `exhttp.ClientSettings.TransportOverride`. No `libgm` fork and no sockets. Covers:
  1. The pairing handshake end to end: `RegisterPhoneRelay` returns a valid `RegisterPhoneRelayResponse` (pairing key, token data, browser device); `/web/config` returns a `Config` payload; the first `ReceiveMessages` long-poll returns a `PairEvent` (`RPCPairData_Paired`) in the `[[ ... ]]` pblite stream.
  2. Post-pairing RPC responses delivered on the `ReceiveMessages` long-poll: `FetchMessages` (`LIST_MESSAGES`) cursor paging, `ListContacts` (`LIST_CONTACTS`), and `IsBugleDefault`.
  3. Media download over `/upload`: encrypted AES-GCM media fixtures returned for registered attachment IDs.
- **Remaining scope**: `RefreshPhoneRelay`, `SignInGaia`/`RegisterRefresh`, `SendMessage`/`AckMessages`, and media upload remain unimplemented and are tracked in issue #62.
- **Verification**: `internal/gm/emulator_e2e_test.go::TestEmulator_StartLoginReturnsQR` and `TestEmulator_PairingCompletesPairDone` drive the real `gm.Client` pairing loop hermetically. `internal/gm/testing/mock_server_test.go::TestMockServer_FetchMessages` and `::TestMockServer_ListContacts` drive the real `gm.Client` end-to-end through the emulator and assert observable paged messages and normalized contacts with zero network access.

### `HARN-03`: Hermetic Test Suite & Live Network Quarantine
- **Category**: `HARN` (`FailClosedGuardrail`)
- **Status**: `[x] Complete` (Issue #63)
- **Description**: Enforces a fully offline default test run. The `gm` package injects a deny-all `http.RoundTripper` through the `libgm` constructor seam (`newLibGMClient`) unless `GM_ALLOW_NETWORK=1`. Any accidental outbound request fails loudly with a guard error. Live-network tests (real Google registration and Gaia pairing) are quarantined behind the `live` build tag and require `LIVE_GM_TEST=1`.
- **Verification**: `internal/gm/hermetic_test.go::TestHermeticGuard_BlocksOutboundRegistration` drives the real `libgm.StartLogin` path and asserts the guard blocks it. `TestDenyNetworkTransport_RejectsRequests` covers the transport directly. The hermetic suite runs in CI, and `make test-live-compile` validates the `live` build without executing it.

### `HARN-04`: Gated Real-Device Live Smoke Test
- **Category**: `HARN` (`PositiveFeature`)
- **Status**: `[ ] In Progress` (Issue #65)
- **Description**: One opt-in end-to-end test that proves the real provider handoff against an operator's Google account. It is quarantined behind the `live` build tag and runs only when `LIVE_GM_TEST=1`, `SESSION_PATH`, and `DATABASE_URL` are set. The test pairs via Gaia emoji (operator approves manually), asserts the session is persisted with POSIX `0600`, calls `Connect`, waits for one real inbound message, and verifies the persistence handoff. It never runs in CI and does not substitute for the hermetic emulator suite.
- **Verification**: `internal/gm/live_smoke_test.go::TestLiveSmoke_EndToEnd` compiles under `-tags live` and is excluded from default CI. Manual run against the operator's number must pass; failure modes must produce actionable errors. `make test-live-compile` validates compilation.

### `UI-01`: Web Management Dashboard
- **Category**: `UI` (`PositiveFeature`)
- **Status**: `[x] Complete` (PR #46, commit `8dfb444`)
- **Description**: Embedded web UI served on `/` showing daemon status, libgm pairing state (with cookie import console), PostgreSQL connectivity and row counts, tracked contact toggles, and recent event logs.
- **Verification**: Browser and handler tests asserting HTML rendering, JSON status polling, and cookie import form interactivity.

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

### `UI-06`: VitePress Google Brand Theme
- **Category**: `UI` (`PositiveFeature`)
- **Status**: `[x] Complete` (Issue #71)
- **Description**: A custom VitePress theme (`docs/.vitepress/theme/index.ts` + `custom.css`) themes the documentation portal with Google's four brand colors: blue `#4285F4` for brand/primary, green `#34A853` for tip/success, yellow `#FBBC05` for warning/highlight, and red `#EA4335` for danger/error. Dark mode uses lighter tints for contrast. The home hero wordmark uses a four-color gradient, and the Mermaid theme is palette-aligned.
- **Verification**: `npm run docs:build` succeeds; light and dark screenshots captured at `assets/screenshots/docs_home_*.png` and `assets/screenshots/docs_guide_*.png` via headless Chromium.

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
- **Verification**: Integration test in `cmd/gmessages-rag/main_test.go::TestBuildServeMux_RouteRegistration` asserting route dispatch; graceful shutdown is exercised by the daemon's signal handling.

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
- **Status**: `[x] Complete` (Issue #79)
- **Description**: Returns all known SMS/RCS conversations with identifiers, participant lists, tracked status, and last activity timestamps. The handler is a thin wrapper over `internal/db` (`ListConversations`).
- **Verification**: Integration test in `internal/mcpserver/tools_test.go::TestTools_ListConversations_RealDB` asserting seeded conversations appear in the tool output.

### `MCP-02`: `get_contact_dossier` Tool
- **Category**: `MCP` (`PositiveFeature`)
- **Status**: `[x] Complete` (Issue #79)
- **Description**: Fetches the structured JSON state, Markdown narrative, and unindexed message tail for a tracked contact. The handler wraps `GetContactDossier` and `GetDossierLiveTail`.
- **Verification**: Integration test in `internal/mcpserver/tools_test.go::TestTools_GetContactDossier_RealDB`.

### `MCP-03`: `search_conversation_history` Tool
- **Category**: `MCP` (`PositiveFeature`)
- **Status**: `[x] Complete` (Issue #79)
- **Description**: Executes Hybrid RRF queries fusing vector cosine similarity and full-text BM25 rankings over session transcripts. The handler wraps `SearchSessionsHybrid`.
- **Verification**: Integration test in `internal/mcpserver/tools_test.go::TestTools_SearchConversationHistory_RealDB`.

### `MCP-04`: `get_messages_around_time` Tool
- **Category**: `MCP` (`PositiveFeature`)
- **Status**: `[x] Complete` (Issue #79)
- **Description**: Retrieves context windows of verbatim messages and OCR annotations surrounding a target timestamp. The handler wraps `GetMessagesAroundTime` and `GetMediaAttachmentsForMessage`.
- **Verification**: Integration test in `internal/mcpserver/tools_test.go::TestTools_GetMessagesAroundTime_RealDB`.

### `MCP-05`: `search_shared_media` Tool
- **Category**: `MCP` (`PositiveFeature`)
- **Status**: `[x] Complete` (Issue #79)
- **Description**: Queries image attachments by OCR extracted text and visual scene descriptions. The handler wraps `SearchMediaAttachments`.
- **Verification**: Integration test in `internal/mcpserver/tools_test.go::TestTools_SearchSharedMedia_RealDB`.

### `MCP-06`: `manage_tracked_contact` Tool
- **Category**: `MCP` (`PositiveFeature`)
- **Status**: `[x] Complete` (Issue #79)
- **Description**: Enables/disables thread tracking, resets historical backfill state, or forces an immediate dossier re-read. The handler wraps `SetConversationTracked`, `ResetConversationBackfill`, and `GetContactDossier`.
- **Verification**: Integration test in `internal/mcpserver/tools_test.go::TestTools_ManageTrackedContact_RealDB` asserting database state deltas.

### `TRANS-01`: Health Check Probe
- **Category**: `TRANS` (`PositiveFeature`)
- **Status**: `[x] Complete` (Commit `57dad5e` / PR #41, commit `151d981`)
- **Description**: Exposes `/health` returning HTTP 200 with database connectivity status and version JSON.
- **Verification**: `internal/obs/health_test.go::TestHealthHandler_Healthy` and route wiring in `cmd/gmessages-rag/main_test.go::TestBuildServeMux_RouteRegistration`.

### `TRANS-02`: Streamable MCP & SSE Endpoints
- **Category**: `TRANS` (`PositiveFeature`)
- **Status**: `[x] Complete` (Commit `57dad5e`)
- **Description**: Exposes Streamable HTTP `/mcp` and Server-Sent Events `/sse` on port 8090.
- **Verification**: `internal/mcpserver/server_test.go::TestProtocol_20260728_PingAndHandshake` and route wiring in `cmd/gmessages-rag/main_test.go::TestBuildServeMux_RouteRegistration`.

### `TRANS-03`: Briefing API & Outbound Webhook
- **Category**: `TRANS` (`PositiveFeature`)
- **Status**: `[x] Complete` (Issue #80)
- **Description**: Exposes `/api/v1/briefing` and `/api/v1/briefing/today` for Home Assistant REST sensors. The endpoint returns, per tracked contact, what changed since a caller-supplied `since` timestamp (RFC3339) or a default 24-hour lookback. Change records are built from `contact_dossier_history` (the preceding version snapshot) and the current `contact_dossiers` state. Dossier-derived Markdown is wrapped in fail-closed untrusted-data boundaries. On a reconciler update, the daemon dispatches a JSON POST to `WEBHOOK_URL` with bounded exponential backoff, optional bearer token (`WEBHOOK_AUTH_TOKEN`) and optional HMAC-SHA256 signature (`WEBHOOK_HMAC_SECRET`).
- **Verification**: `internal/web/briefing_test.go::TestBriefingAPI_ChangedSince` (real Postgres, proves the response reflects real history changes and boundary wrapping) and `internal/notify/webhook_test.go::TestWebhookDispatch_RetriesOnFailure` (httptest contract stub proves retried deliveries). Route wiring in `cmd/gmessages-rag/main_test.go::TestBuildServeMux_RouteRegistration`.

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

---

## 2026-11-02 Traceability Reconciliation

A code audit found the SRS had marked several unbuilt behaviors `[x] Complete`, referencing test
paths that did not exist. The following rows were corrected in the matrix. Every `[x] Complete`
row must now point at a test that exists; CI enforces this via `scripts/check_traceability.sh`.

| Requirement | Was | Now | Reason |
| :--- | :--- | :--- | :--- |
| `PIPE-01` | Complete | Not Started | No vision/OCR client exists; `VISION_MODEL` unused |
| `PIPE-02` | Complete | Partial | Debouncer tested, but daemon `OnFlush` only logs |
| `PIPE-03` | Complete | Not Started | No LLM/dossier engine; `DOSSIER_MODEL` unused |
| `PIPE-04` | Complete | Partial | Contacts fetch/parse real; no dossier enrichment |
| `PIPE-05` | Complete | Partial | `RecordTypingType` exists but is never called |
| `PIPE-06` | Complete | Partial | Message ETL real; no embedding/vector staging |
| `SEC-01` | Complete | Partial | Decrypt/hash/store tested; no ingestion caller |
| `HARN-01` | Complete | Complete | Mock server `Settings()`/`Transport()` consumed by real `gm.Client`; disturbance proof added (#82) |
| `AUTH-04` | Complete | Partial | Success path needs live smoke; proof path did not exist |
| `MCP-01`..`MCP-06` | Partial | Complete | Handlers rewritten as DB-backed wrappers over `internal/db`; real-Postgres proof tests added (Issue #79) |
| `TRANS-03` | Complete | Not Started | No briefing route and no webhook dispatch |
| `UI-02`,`UI-05` | Complete | Partial | Panels work; embedded MCP calls resolve to stubs |
| `UI-01`,`DAEMON-01`,`TRANS-01`,`TRANS-02` | Complete | Complete | Status correct; proof paths corrected to existing tests |
| `DB-07` | In Progress | Complete | CI Postgres service merged (PR #73) |
| `HARN-02` | In Progress | Complete | FetchMessages/ListContacts/media emulated; real `gm.Client` proofs added (#82) |

### 2026-11-02 Resolutions (Issue #78)

The table above is a point-in-time audit snapshot. Subsequent work re-promotes rows whose
implementation and proof now exist.

| Requirement | Audit State | Current State | Resolution |
| :--- | :--- | :--- | :--- |
| `PIPE-03` | Not Started | `[x] Complete` | Living Dossier reconciler landed: `internal/pipeline/dossier.go`, `internal/pipeline/dossier_state.go`; proof `internal/pipeline/dossier_test.go::TestDossierReconciliation_DateAnchoring` |
| `PIPE-04` | Partial | `[x] Complete` | Contact enrichment consumer landed: `internal/pipeline/contacts.go`; proof `internal/pipeline/contacts_test.go::TestContactsSync_DossierEnrichment` |

### 2026-11-02 Resolutions (Issue #81)

| Requirement | Audit State | Current State | Resolution |
| :--- | :--- | :--- | :--- |
| `PIPE-02` | Partial | `[x] Complete` | Daemon `OnFlush` runs Vision OCR, sessionization, and Dossier reconciliation; proof `internal/pipeline/debouncer_test.go::TestDebouncer_ResetOnNewMessage` |
| `PIPE-05` | Partial | `[x] Complete` | `gm.Client` forwards `*gmproto.TypingData` to `debouncer.RecordTypingType` via `OnTypingActivity`; proofs `internal/pipeline/debouncer_test.go::TestDebouncer_TypingExtendsWindow`, `internal/gm/client_test.go::TestClient_TypingEventDispatch` |
| `SEC-01` | Partial | `[x] Complete` | ETL ingestion persists decrypted media through `pipeline.MediaIngestor` → `db.UpsertMediaAttachment`; proofs `internal/gm/media_test.go::TestMediaDecryption_AESGCM`, `internal/pipeline/media_test.go::TestMediaIngestor_PersistsDecryptedMedia` |

### 2026-11-02 Resolutions (Issue #80)

| Requirement | Audit State | Current State | Resolution |
| :--- | :--- | :--- | :--- |
| `TRANS-03` | Not Started | `[x] Complete` | Briefing API and outbound webhook landed (Issue #80): `internal/web/briefing.go`, `internal/notify/webhook.go`, `internal/db/briefing.go`; proofs `internal/web/briefing_test.go::TestBriefingAPI_ChangedSince` and `internal/notify/webhook_test.go::TestWebhookDispatch_RetriesOnFailure` |
