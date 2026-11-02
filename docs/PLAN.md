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
- [x] **Issue #39 (`PIPE-04`)**: Google Contacts synchronization (`ListContacts`) in `internal/gm/contacts.go`. *(Merged PR #47 - commit `836834b`)*
- [x] **Issue #23 (`PIPE-06`)**: Standardized 3-stage batch ETL ingestion pipeline (`Extract`, `Transform`, `Load`) in `internal/pipeline/etl.go`. *(Merged PR #48 - commit `badf3e9`)*
- [ ] **Issue #32 (`INT-01`)**: Bi-directional Home Assistant MQTT bridge and auto-discovery in `internal/mqtt/client.go`. *(Note: Deferred/held in favor of universal Apprise/ntfy push notifications and Prometheus metrics)*
- [x] **Issue #17 (`UI-01`)**: Web Management Dashboard and live status visualizer in `internal/web/dashboard.go`. *(Merged PR #46 - commit `8dfb444`)*
- [x] **Issue #18 (`UI-02`)**: Developer Diagnostic Test Bench for MCP tools and Hybrid RRF sandbox in `internal/web/testbench.go`. *(Merged PR #45 - commit `91e8fcc`)*
- [x] **Issue #20 (`UI-03`)**: Parameter and pipeline hot-reload runtime tuner in `internal/config/tuner.go`. *(Merged PR #44 - commit `2407ed6`)*

---

## Phase 2: Integration & Test Phase (Controls-Grade Verification)
*Focus: Establish a zero-theatre controls-grade simulation harness, enforce static security scanning, and validate concurrency invariants under synthetic disturbance.*

- [x] **Protobuf Mock GM Server (`HARN-01` / Issue #29)**:
  - Implemented `internal/gm/testing/mock_server.go` and `mock_server_test.go` with disturbance injection (socket drops, HTTP 503 errors, corrupt payloads, network delays). *(Merged - commit `ecac8e2`)*
- [x] **Security & Vulnerability Auditing (`SEC-03` / Issue #30)**:
  - Integrated `govulncheck` into `Makefile` (`make vulncheck`) and GitHub Actions CI workflow.
  - Zero data races enforced with `go test -v -race ./...`. *(Merged - commit `30b0cb6`)*
- [x] **Continuous Stream Leak & Stress Verification**:
  - Verified zero memory/goroutine leaks over streaming sessions with `go.uber.org/goleak` and disturbance injection. *(Completed - commits `ecac8e2`, `c279ebd`)*

---

## Phase 3: Comprehensive Refactor, Code Smells Purge & Setup-Specific Design (`REFACTOR-01` / Issue #42)
*Focus: Perform a holistic repository health audit, eliminate code smells from rapid agentic feature delivery, wire the end-to-end daemon, and configure home-lab environment specifics.* *(Completed - commit `c279ebd`)*

- [x] **3.1 Repository Health & Code Smells Audit (`CLEANUP-01`)**:
  - Audited all packages for orphaned helpers, duplicate models, and dead functions across parallel agent branches.
  - Standardized error handling: verified `%w` error wrapping, eliminated raw ignores, guaranteed `defer file.Close()` with error propagation on writes.
  - Audited goroutine lifecycles and channels with `go.uber.org/goleak` to guarantee clean termination on context cancellation. *(Completed - commit `c279ebd`)*
- [x] **3.2 End-to-End Daemon Wiring (`DAEMON-01`)**:
  - Wired CLI commands (`serve` and `pair`) in `cmd/gmessages-rag`.
  - Unified `http.ServeMux` routing `/`, `/dashboard`, `/pair`, `/api/v1/*`, `/testbench`, `/mcp`, `/sse`, `/metrics`, and `/healthz` on port 8090.
  - Connected `SmartDebouncer`, `Watchdog`, and background `ETLPipeline` synchronization for tracked contacts.
  - Handled OS signals (`SIGINT`, `SIGTERM`) with graceful connection drain and state persistence. *(Completed - commit `c279ebd`)*
- [x] **3.3 Home-Lab & Container Specific Design (`DOCKER-01`)**:
  - Hardened Dockerfile with multi-stage non-root Alpine container and `PUID`/`PGID` environment mapping.
  - Configured `docker-compose.yml` with persistent volume bind-mounts for `/data/auth`, `/data/media`, and `/data/dossiers`.
  - Documented `.env.example` with production configuration parameters. *(Completed - commit `c279ebd`)*

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
- [x] Update `docs/REQUIREMENTS.md` with commit hashes and PR links for all requirements.
- [x] Update `docs/ARCHITECTURE.md` with the finalized unified daemon topology and ServeMux routing.
- [x] Finalize `docs/CHANGELOG.md` under `[1.0.0-rc1] - 2026-11-02`.
- [ ] Update root `README.md` with visual architecture badges, Docker quickstart, environment variable matrix, and docs link.

### 4.3 Versioned Release (`v1.0.0-rc1` / `v1.0.0`)
- Tag repository and build multi-arch binaries (`linux/amd64`, `linux/arm64`) and Docker images.
