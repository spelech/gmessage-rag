# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- Integrated Steven T. Pelech's `AgenticEngineeringToolbelt` submodule (`.toolbelt`) with active skills: `engineering-archetype`, `scaffold-project`, `test-harness-builder`, `code-review-foci`, and `mermaid-live`.
- Linked universal and agent-specific instruction rules: `AGENTS.md`, `GEMINI.md`, and `CLAUDE.md`.
- Established traditional Git Flow branch architecture (`main`, `develop`, and `feature/*`).
- Configured 4-stage GitHub Actions CI pipeline (`.github/workflows/ci.yml`) and Dependabot updates (`.github/dependabot.yml`).
- Authored comprehensive living documentation: `docs/ARCHITECTURE.md` (topology, ER diagram, sequence flow) and `docs/REQUIREMENTS.md` (SRS with category taxonomy and test traceability matrix).
- Added `scripts/commit.sh` enforcing Git Flow branch protection and Conventional Commits.
- Implemented `internal/mcpserver` adhering strictly to the `2026-07-28` Model Context Protocol (MCP) specification with native `ping` JSON-RPC method, `Mcp-Protocol-Version: 2026-07-28` response header, Streamable HTTP (`POST /mcp`), Server-Sent Events (`GET /sse`), stdio transport, and prompt injection XML boundary guardrails (`GUARD-01`, `GUARD-02`, `MCP-00`).
- Implemented `internal/gm` (`libgm` client wrapper, dual QR terminal & Web pairing, microsecond/millisecond timestamp magnitude normalization, direction resolution, and atomic POSIX 0600 session token persistence resolving Issue #7).
- Implemented `internal/db/sessions.go` (2-hour session window grouping, calendar-anchored transcript rendering, and Hybrid RRF Reciprocal Rank Fusion search query CTE resolving Issue #5).
- Implemented `internal/db/messages.go`, `conversations.go`, `types.go`, and `messages_test.go` (488 LOC) with idempotent raw message and conversation CRUD, batch boundaries, and atomic reaction deduplication (resolving Issue #4).
- Implemented `internal/db/dossiers.go` and `dossiers_test.go` (326 LOC) with Living Dossier persistence, semantic diffing, audit history snapshotting, and timeline live tails (resolving Issue #6).
- Documented single-device Google Account (Gaia) emoji challenge pairing flow (`AUTH-04`) eliminating camera QR scan requirements on single-screen devices.
- Created `docs/NOTES.md` documenting Go architectural patterns, SOLID translations, concurrency state models, language selection decision matrix, and `golang-service-daemon` blueprint.
- Specified advanced capabilities across requirements and architecture: Apprise/ntfy mobile re-auth alerting (`AUTH-03`), inferred read receipt heuristics for SMS/iPhone contacts (`DB-06`), opt-in full-resolution media retrieval (`SEC-02`), Google Contacts profile enrichment (`PIPE-04`), action-aware smart debouncing on typing events (`PIPE-05`), standardized 3-stage batch ETL ingestion (`PIPE-06`), and debounced phone connectivity monitoring (`TRANS-04`).
- Implemented response-inferred delivery and read receipt heuristics for SMS and cross-carrier RCS threads (`internal/db/receipts.go`, `receipts_test.go`) with `BugleStatusDeliveredInferred` (108), `InferDeliveryReceipts`, and `GetPendingUnacknowledgedMessages` (`DB-06`).
- Implemented `internal/gm/media.go`, `backfill.go`, and `internal/db/media.go` with streaming SHA-256 content-addressed media storage, duplicate file write avoidance, rate-limited cursor pagination backfill, and anti-test-theatre unit tests (resolving Issue #8).
- Documented testing and operational standards: Controls-grade protobuf disturbance simulation fixture (`HARN-01`), CI `govulncheck` call-graph vulnerability scanning and ThreadSanitizer race detection (`SEC-03`), Prometheus `/metrics` and `/healthz` observability endpoints (`OBS-01`), and bi-directional Home Assistant MQTT bridge (`INT-01`).




