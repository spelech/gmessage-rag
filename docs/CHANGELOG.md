# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [1.0.0] - 2026-11-04

### Changed
- **Cookie Import Pairing (`AUTH-02`)**: Replaced QR device pairing with Matrix-style Google session cookie import. The CLI (`gmessages-rag pair --cookies`) and web dashboard (`GET/POST /pair`) accept a cURL command, JSON map/array, raw `Cookie:` header, or semicolon pairs. The importer validates the required Google cookies and starts the Gaia emoji challenge.

### Added
- **Periodic Connection Windows (`SYNC-01`, #102)**: New `SYNC_MODE=periodic` connects for `SYNC_WINDOW_MINUTES` every `SYNC_INTERVAL_MINUTES`, drains tracked conversations incrementally with `ETLPipeline.SyncBatch`, then disconnects without clearing the session cookie. `GM_PING_INTERVAL_MINUTES` and `GM_ALERT_TIMEOUT_COUNT` reduce ping wakeups while keeping disconnect detection inside the watchdog grace. `SYNC_MODE=continuous` preserves the previous always-connected behavior.
- **DB-Backed MCP Tool Handlers (`MCP-01`..`MCP-06`, #79)**: All six builtin MCP tools now execute real queries through `internal/db` (`ListConversations`, `GetContactDossier`/`GetDossierLiveTail`, `SearchSessionsHybrid`, `GetMessagesAroundTime`, `SearchMediaAttachments`, `SetConversationTracked`/`ResetConversationBackfill`) instead of placeholder stubs. Added optional `MCP_ACCESS_TOKEN` bearer enforcement on `tools/call` and a `db.SearchMediaAttachments` query.
- **Real-Postgres MCP Integration Tests**: `internal/mcpserver/tools_test.go` asserts seeded records flow through the JSON-RPC tool surface (`TestTools_*_RealDB`), skipping cleanly when `DATABASE_URL` is unavailable.

## [1.0.0-rc2] - 2026-11-02

### Added
- **Multi-Tab Dashboard & Diagnostic Panels (`UI-05`, #59)**: Embedded interactive Diagnostic Test Bench and Runtime Tuner panels directly into the Web Management Dashboard via 3-tab navigation (`Overview`, `Diagnostic Test Bench`, `Runtime Tuner`).
- **Automated Chromedp Layout Audit & Visual Regression Harness (`TEST-04`, #57)**: Pure-Go `chromedp` visual regression and ergonomics audit (`internal/web/layout_audit_test.go`) validating zero horizontal overflow, $\ge 44\text{px}$ touch targets, and dark/light theme rendering across mobile, tablet, and desktop breakpoints.
- **Dashboard Accessibility & Inline Error Recovery (`UI-04`, #58)**: WCAG 2.1 AA focus rings, `table-responsive` mobile overflow wrappers, `aria-live="polite"` real-time status badges, and retryable network error banners.
- **GHCR Container Packaging & Release Pipeline (`CI-03`)**: Added `.github/workflows/release.yml` automating container build and publishing to GitHub Container Registry (`ghcr.io/spelech/gmessage-rag:v1.0.0-rc2`) on release tags.

### Fixed
- **Daemon Lifecycle Context Propagation (`DAEMON-01`, `TEST-03`)**: Propagated Cobra `cmd.Context()` and `SESSION_PATH` environment overrides in `runServe` for deterministic HTTP connection draining and testing.
- **Quality & Concurrency Invariants**: Maintained $\ge 80\%$ statement coverage across all packages with 0 data races in `go test -race ./...`.

## [1.0.0-rc1] - 2026-11-02

### Added
- **Single-Device Gaia Emoji Pairing (`AUTH-04`)**: Google Account (Gaia) emoji challenge flow (`libgm.Client.DoGaiaPairing`) enabling pairing on single-screen mobile devices without camera QR scanning (PR #36, commit `fd1f7a3`).
- **Standardized 3-Stage Batch ETL Pipeline (`PIPE-06`)**: Cursor-paginated message backfill pipeline (`Extract`, `Transform`, `Load`) in `internal/pipeline/etl.go` guaranteeing bounded memory consumption and transactional batch persistence (PR #48, commit `badf3e9`).
- **Google Contacts Synchronization (`PIPE-04`)**: Contact roster enrichment via `libgm.Client.ListContacts` and `ListTopContacts` (`internal/gm/contacts.go`) integrating real names, avatars, and phone labels into Living Dossiers (PR #47, commit `836834b`).
- **Web Management Dashboard & Visualizer (`UI-01`)**: Embedded SPA served on `/` and `/dashboard` providing live daemon status, database connectivity metrics, tracked contact toggle table, and pairing QR visualizer (PR #46, commit `8dfb444`).
- **Context-Cortex Style Diagnostic Test Bench (`UI-02`)**: Developer workbench at `/testbench` and `/api/v1/testbench/*` for ad-hoc MCP tool execution, `<untrusted_conversation_data>` boundary verification, Hybrid RRF scoring analysis, and OCR simulations (PR #45, commit `91e8fcc`).
- **Parameter & Pipeline Runtime Tuner (`UI-03`)**: Dynamic hot-reload tuner (`internal/config/tuner.go`, `/api/v1/tuner`) to inspect and update runtime variables (debouncer duration, RRF $k$ weight, backfill batch sizes) without restarting the container (PR #44, commit `2407ed6`).
- **Dashboard Usability & Accessibility Enhancement (`UI-04`)**: Responsive table overflow wrapper, CSS clamp/minmax cards, WCAG 2.1 AA ARIA attributes, >=44px touch targets, and inline network error recovery banner in Web Management Dashboard (`internal/web/assets/index.html`).
- **Standardized Prometheus Metrics & Health Probes (`OBS-01`)**: Prometheus metrics registry (`/metrics`) and health check handler (`/healthz`, `/health`) monitoring ingestion counters, session gauges, and database pool reachability (PR #41, commit `151d981`).
- **Default Gaia Emoji Pairing & QR Demotion (`AUTH-05`)**: Made Google Account (Gaia) emoji challenge pairing the default path for CLI `pair` and web `GET /pair`; demoted legacy QR code scanning to `--qr` and `?mode=qr` (PR #50).
- **Action-Aware Smart Debouncer (`PIPE-05`)**: Enhanced idle debouncer in `internal/pipeline/debouncer.go` listening to participant typing indicators (`UpdateEvents_TypingEvent`) and outbound drafts to prevent splitting active conversation turns (PR #40, commit `09c2323`).
- **Full-Resolution Media Retrieval (`SEC-02`)**: Opt-in `PULL_FULL_SIZE_MEDIA` flag calling `libgm.Client.GetFullSizeImage` before Vision OCR for high-clarity document analysis (PR #38, commit `8d5b7e4`).
- **Inferred Read & Delivery Receipt Heuristics (`DB-06`)**: Response-inferred delivery state tracking in `internal/db/receipts.go` inferring preceding message delivery for SMS and cross-carrier RCS contacts lacking native receipts (PR #37, commit `0c88b51`).
- **Universal Push Alerting & Connection Watchdog (`NOTIF-01`, `NOTIF-02`, `TRANS-04`)**: Apprise and ntfy push alerting for mobile session re-auth and 15-minute debounced connection watchdog eliminating cell drop alert fatigue (PR #35, commit `796debd`).
- **Streaming Media Decryption & Content-Addressed Storage (`SEC-01`)**: AES-GCM media decryption with two-tier SHA-256 storage (`/data/media/xx/yy/<hash>.<ext>`) and duplicate avoidance (PR #34, commit `1141504`).
- **Controls-Grade Protobuf Disturbance Simulation Fixture (`HARN-01`)**: In-memory Google Messages WebSocket simulation server (`internal/gm/testing/mock_server.go`) injecting synthetic disconnects, bursty media, and corrupt payloads for hermetic CI testing (Issue #29, commit `ecac8e2`).
- **MCP 2026-07-28 Protocol Support (`MCP-00`..`06`)**: Full adherence to the 2026-07-28 Model Context Protocol specification with Streamable HTTP (`POST /mcp`), Server-Sent Events (`GET /sse`), stdio transport, ping JSON-RPC method, and 6 core conversation/dossier tools (commit `57dad5e`).
- **Living Dossier Engine & History Snapshotting (`DB-05`, `PIPE-03`)**: Continuous LLM state reconciliation, temporal statement normalization, versioned audit snapshots (`contact_dossier_history`), and Markdown exports (`/data/dossiers/<contact>.md`) (PR #28, commit `af3fed9`).
- **PostgreSQL 17 + pgvector Halfvec HNSW Storage (`DB-01`..`04`)**: Schema migrations, contiguous 2-hour window grouping, and Hybrid RRF search CTE fusing full-text BM25 and vector cosine similarity (commit `789964c`).

### Changed
- **Unified Daemon Orchestration (`DAEMON-01`)**: Replaced split server entrypoints with a unified root `http.ServeMux` in `cmd/gmessages-rag/serve.go` routing UI, REST API, Test Bench, MCP, Prometheus metrics, and health probes over a single port (Issue #42, commit `c279ebd`).
- **Graceful Lifecycle Shutdown**: Integrated `os/signal` context cancellation (`SIGINT`, `SIGTERM`) across HTTP server, background ETL sync, watchdog workers, and PostgreSQL connection pool with orderly 10-second drain (Issue #42, commit `c279ebd`).
- **Project Structure Alignment**: Aligned CLI commands under `cmd/gmessages-rag/main.go`, `serve.go`, and `pair.go` with Cobra command architecture.

### Fixed
- **Dead Code and Code Smells Purge (`CLEANUP-01`, `REFACTOR-01`)**: Removed orphaned types and duplicate models across feature branches, converted raw error returns to wrapped `%w` formats, eliminated unchecked file close calls, and added `goleak` goroutine leak validation (Issue #42, commit `c279ebd`).
- **Timestamp Normalization**: Handled inconsistent microsecond vs millisecond timestamp scales emitted across different Android Google Messages versions.

### Security
- **Fail-Closed Prompt Injection Boundaries (`GUARD-01`)**: Wrapped all external SMS/RCS texts, transcripts, and OCR outputs inside `<untrusted_conversation_data>` XML guardrail tags across all MCP tool responses (commit `57dad5e`).
- **Send Message Tool Guard (`GUARD-02`)**: Restricted outbound message sending behind `ENABLE_MCP_SEND_MESSAGE=true` to prevent unauthorized message dispatch.
- **POSIX 0600 Session Permissions (`AUTH-01`)**: Enforced strict POSIX `0600` file modes for session credential storage at `/data/auth/session.json`.
- **CI Static Vulnerability Auditing & Race Detection (`SEC-03`)**: Integrated `govulncheck` static call-graph vulnerability scanning and `go test -race` concurrency validation in automated CI gates (Issue #30, commit `30b0cb6`).
- **Docker Hardening (`DOCKER-01`)**: Multi-stage Alpine container build running under unprivileged non-root user with `PUID`/`PGID` permission mapping and immutable volume mounts (Issue #42, commit `c279ebd`).
