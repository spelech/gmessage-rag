# gmessages-rag Master Implementation, Integration & Release Plan

## Executive Summary
This roadmap establishes the phased path from active feature implementation to a verified, hardened, production-ready release of `gmessages-rag` for personal home-lab and agentic deployment.

---

## Phase 1: Feature Implementation & Jules Parallel Execution
*Focus: Complete remaining core pipeline and protocol capabilities via Google Labs Jules parallel workers and local integration without branch drift.*

- [x] **PR #34 (`SEC-01`, `PIPE-03`)**: Streaming AES-GCM media decryption, two-tier content-addressed `/data/media/xx/yy/<hash>.<ext>` storage, POSIX 0600 permissions, cursor-based historical pagination. *(Merged - commit `1141504`)*
- [x] **PR #35 (`NOTIF-01`, `NOTIF-02`)**: Multi-channel push alerting (Apprise & ntfy) for mobile QR/Gaia re-auth and 15-minute debounced connection watchdog. *(Merged - commit `796debd`)*
- [x] **PR #36 (`AUTH-04`)**: Single-device Google Account (Gaia) emoji challenge pairing without camera QR scan. *(Merged - commit `fd1f7a3`)*
- [x] **PR #37 (`DB-06`)**: Inferred read & delivery receipts heuristic for SMS/iPhone contacts based on subsequent incoming messages. *(Merged - commit `0c88b51`)*
- [x] **PR #38 (`SEC-02`)**: Opt-in full-resolution media retrieval flag (`PULL_FULL_SIZE_MEDIA`) via `libgm.Client.GetFullSizeImage` with fallback. *(Merged - commit `8d5b7e4`)*
- [x] **PR #40 (`PIPE-05`)**: Action-aware smart debouncer (`internal/pipeline/debouncer.go`) for session boundary chunking. *(Merged - commit `09c2323`)*
- [x] **PR #41 (`OBS-01`)**: Standardized Prometheus metrics (`/metrics`) and health check probes (`/healthz`) in `internal/obs/`. *(Merged - commit `151d981`)*
- [ ] **Issue #39 (`PIPE-04`)**: Google Contacts enrichment (`ListContacts`) in `internal/gm/contacts.go`. *(Jules Task 10608232599014912140 in progress)*
- [ ] **Issue #23 (`PIPE-06`)**: Standardized 3-stage batch ETL ingestion pipeline (`Extract`, `Transform`, `Load`) in `internal/pipeline/etl.go`. *(Jules Task 8801252328886619841 in progress)*
- [ ] **Issue #32 (`INT-01`)**: Bi-directional Home Assistant MQTT bridge and auto-discovery in `internal/mqtt/client.go`. *(Jules Task 10612730918939830900 in progress)*
- [ ] **Issue #17 (`UI-01`)**: Web Management Dashboard and live status visualizer in `internal/web/dashboard.go`. *(Jules Task 8343057452248871363 in progress)*
- [ ] **Issue #18 (`UI-02`)**: Developer Diagnostic Test Bench for MCP tools and Hybrid RRF sandbox in `internal/web/testbench.go`. *(Jules Task 15422789838467041322 in progress)*
- [ ] **Issue #20 (`UI-03`)**: Parameter and pipeline hot-reload runtime tuner in `internal/config/tuner.go`. *(Jules Task 16384505810344623607 in progress)*

---

## Phase 2: Integration & Test Phase (Controls-Grade Verification)
*Focus: Establish a zero-theatre controls-grade simulation harness, enforce static security scanning, and validate concurrency invariants under synthetic disturbance.*

- [x] **Protobuf Mock GM Server (`HARN-01` / Issue #29)**:
  - Implemented `internal/gm/testing/mock_server.go` and `mock_server_test.go` with disturbance injection (socket drops, HTTP 503 errors, corrupt payloads, network delays). *(Merged - commit `ecac8e2`)*
- [x] **Security & Vulnerability Auditing (`SEC-03` / Issue #30)**:
  - Integrated `govulncheck` into `Makefile` (`make vulncheck`) and GitHub Actions CI workflow.
  - Zero data races enforced with `go test -v -race ./...`. *(Merged - commit `30b0cb6`)*
- [ ] **Continuous Stream Leak & Stress Verification**:
  - Run synthetic load test against `MockGMServer` verifying zero memory/goroutine leaks over long streaming periods.

---

## Phase 3: Comprehensive Refactor, Code Smells Purge & Setup-Specific Design (`REFACTOR-01` / Issue #42)
*Focus: Perform a holistic repository health audit, eliminate code smells from rapid agentic feature delivery, wire the end-to-end daemon, and configure home-lab environment specifics.*

### 3.1 Repository Health & Code Smells Audit
- **Dead Code & Duplication Elimination**:
  - Audit all packages for orphaned helpers, duplicate models, or dead functions created across parallel agent branches.
  - Standardize error handling: verify `%w` error wrapping, eliminate raw ignores (`_ = ...`), guarantee `defer file.Close()` with error propagation on writes.
  - Audit goroutine lifecycles and channels: ensure all background workers terminate cleanly on context cancellation (`go.uber.org/goleak`).
- **Package Architecture & Clean Boundaries**:
  - Verify clean unidirectional package imports (zero cyclic dependencies).
  - Ensure strict interface boundaries for testability and isolation.

### 3.2 End-to-End Daemon Wiring (`cmd/gmessages-rag/main.go`)
- **`serve` Command**:
  - Initialize Postgres/pgvector connection pool with automated migration execution.
  - Start GM client listener with connection watchdog (`Watchdog`).
  - Connect `SmartDebouncer` for action-aware participant typing event boundaries.
  - Start background ETL worker for tracked contact threads (`TRACKED_CONTACTS`).
  - Mount Prometheus `/metrics`, `/healthz`, and embedded Web UI (`UI-01` to `UI-03`).
  - Attach optional Home Assistant MQTT bridge (`INT-01`).
  - Handle OS signals (`SIGINT`, `SIGTERM`) with graceful connection drain and state persistence.
- **`pair` Command**:
  - Connect terminal QR visualizer and single-device Gaia emoji pairing challenge with signal cancellation.

### 3.3 Home-Lab & Container Specific Design
- Docker Compose configuration:
  - Persistent volume mounts for `/data/auth`, `/data/media`, and `/data/dossiers`.
  - Explicit UID/GID (`PUID`/`PGID`) mapping matching host storage permissions.
  - Production-ready `.env.example` with documented defaults.

---

## Phase 4: Documentation Sweep, VitePress Site & Versioned Release (`DOCS-01` / Issue #43)
*Focus: Deploy a modern VitePress documentation site with Mermaid and LaTeX support, synchronize all living documents, and produce the release candidate.*

### 4.1 VitePress Documentation Site (`docs/.vitepress/`)
- Modern documentation portal powered by VitePress:
  - **Mermaid Diagram Support**: Interactive diagram rendering via `vitepress-plugin-mermaid` for dataflows, topologies, and sequence diagrams.
  - **LaTeX / Math Support**: Mathematical formula and KaTeX rendering (`markdown-it-mathjax3` / KaTeX) for Hybrid RRF formulas and vector distance metrics.
  - **Comprehensive Navigation**:
    - System Architecture & Concurrency Model
    - Requirements Traceability Matrix (`REQUIREMENTS.md`)
    - Quickstart & Docker Home-Lab Deployment Guide
    - Agent-Native MCP Specification (`2026-07-28`)
    - Living Dossier & Hybrid RAG Deep-Dive
    - Observability, Prometheus Metrics & Health Probes

### 4.2 Living Documentation Sweep
- Update `docs/REQUIREMENTS.md` with commit hashes and PR links for all requirements.
- Update `docs/ARCHITECTURE.md` with the finalized unified daemon topology and MQTT bridge interfaces.
- Finalize `docs/CHANGELOG.md` under `[1.0.0-rc1]`.
- Update root `README.md` with visual architecture badges, Docker quickstart, and configuration reference.

### 4.3 Versioned Release (`v1.0.0-rc1` / `v1.0.0`)
- Tag repository and build multi-arch binaries (`linux/amd64`, `linux/arm64`) and Docker images.
