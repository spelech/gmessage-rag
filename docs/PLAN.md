# gmessages-rag Master Implementation, Integration & Release Plan

## Executive Summary
This roadmap establishes the phased path from active feature implementation to a verified, hardened, production-ready release of `gmessages-rag` for personal home-lab and agentic deployment.

---

## Phase 1: Feature Implementation & Jules Parallel Execution
*Focus: Complete remaining core pipeline and protocol capabilities via Google Labs Jules parallel workers and local integration without branch drift.*

- [x] **PR #34 (`SEC-01`, `PIPE-03`)**: Streaming AES-GCM media decryption, two-tier content-addressed `/data/media/xx/yy/<hash>.<ext>` storage, POSIX 0600 permissions, cursor-based historical pagination. *(Merged)*
- [x] **PR #35 (`NOTIF-01`, `NOTIF-02`)**: Multi-channel push alerting (Apprise & ntfy) for mobile QR/Gaia re-auth and 15-minute debounced connection watchdog. *(Merged)*
- [ ] **PR #36 (`AUTH-04`)**: Single-device Google Account (Gaia) emoji challenge pairing without camera QR scan. *(Testing passed with zero races; ready to merge)*
- [ ] **Issue #21 (`DB-06`)**: Inferred read & delivery receipts heuristic for SMS/iPhone contacts based on subsequent incoming messages. *(Jules Task 3131410209681617493 in progress)*
- [ ] **Issue #22 (`SEC-02`)**: Opt-in full-resolution media retrieval flag (`PULL_FULL_SIZE_MEDIA`) via `libgm.Client.GetFullSizeImage` with fallback. *(Jules Task 4715540346075184621 in progress)*
- [ ] **Issue #24 (`PIPE-04`, `PIPE-05`)**: Google Contacts enrichment (`ListContacts`) and action-aware typing event debouncer for session boundaries. *(Jules Task 3864161541733976186 in progress)*
- [ ] **Issue #23 (`PIPE-06`)**: Standardized 3-stage batch ETL ingestion pipeline (Extract, Transform, Load) for threads exceeding 10,000 messages. *(Dispatch to Jules once Issue #21 receipts merge)*

---

## Phase 2: Integration & Test Phase (Controls-Grade Verification)
*Focus: Establish a zero-theatre controls-grade simulation harness, enforce static security scanning, and validate concurrency invariants under synthetic disturbance.*

### 2.1 Simulation Harness (`HARN-01` / Issue #29)
- **Protobuf Mock GM Server (`internal/gm/testing/mock_server.go`)**:
  - In-memory mock server mimicking Google Messages web backend SSE and HTTP RPC endpoints (`/bind`, `/channel`).
  - Synthetic event generators emitting realistic protobuf sequences (`gmproto.UpdateEvents`, `TypingEvent`, multi-recipient RCS/SMS, media attachments, delivery receipts).
- **Disturbance Injection Suite**:
  - Socket drops and unexpected disconnects.
  - Reconnect storms and out-of-order pagination cursors.
  - Corrupted media payloads and decryption failures.
- **Diagnostic Tap Points**:
  - State inspection hooks for session chunker, debouncer timers, and backfill progress.

### 2.2 Security & Thread Sanitizer Suite (`SEC-03` / Issue #30)
- **Vulnerability Auditing (`govulncheck`)**:
  - Integrate `golang.org/x/vuln/cmd/govulncheck` into `Makefile` and GitHub Actions CI workflow.
  - Fail pipeline on known CVEs in transitive dependencies.
- **Race Detection & Stress Testing**:
  - Enforce `go test -v -race ./...` on all packages.
  - Add parallel stress test with high concurrency (100 simultaneous simulated conversations).

### 2.3 End-to-End Pipeline Verification
- Execute mock backfill -> session chunking -> dossier generation pipeline.
- Verify memory stability (zero heap leak under continuous streaming).
- Verify POSIX permission enforcement across auth tokens (`0600`) and media directories (`0700`).

---

## Phase 3: Refactor & Setup-Specific Design Items
*Focus: Optimize configuration, hardware volume mounts, Home Assistant home automation integration, and production observability.*

### 3.1 Host & Container Bindings
- **Docker Compose Configuration**:
  - Persistent volume mounts for `/data/auth` (Gaia tokens / session store) and `/data/media` (content-addressed media store).
  - Explicit UID/GID mapping (`PUID`/`PGID`) to match host storage permissions.
  - Resource limits (memory limits, health check intervals).

### 3.2 Home Assistant MQTT Bridge (`INT-01` / Issue #32)
- **MQTT Auto-Discovery**:
  - Publish MQTT discovery payloads for Home Assistant entities:
    - Connection state sensor (`online`, `offline`, `re-auth required`).
    - Active conversation count and daily message counters.
    - Gaia pairing challenge emoji attribute sensor.
- **Bi-directional Control**:
  - Listen to MQTT command topics for triggering manual backfills, reconnection retries, or test tap triggers.

### 3.3 Production Observability (`OBS-01` / Issue #31)
- **Prometheus Metrics (`/metrics`)**:
  - Ingestion throughput (`messages_ingested_total` by direction/protocol: SMS vs RCS).
  - Backfill latency and message processing durations.
  - Active sessionizer timers and debouncer queue depth.
  - Media decryption success/failure counters.
- **Health Probes (`/healthz`)**:
  - `/healthz/live`: HTTP server liveness probe.
  - `/healthz/ready`: SQLite database connectivity and active Google Messages connection check.

---

## Phase 4: Documentation Sweep, Living Docs & Versioned Release
*Focus: Ensure documentation traceability, living sync, and release automation.*

### 4.1 Living Documentation Sync
- **Requirements Traceability (`docs/REQUIREMENTS.md`)**:
  - Update all requirements (`HARN-01`, `SEC-03`, `INT-01`, `OBS-01`, `PIPE-06`, etc.) with direct links to merged PRs and commit hashes.
- **Architecture Specification (`docs/ARCHITECTURE.md`)**:
  - Diagram the complete end-to-end dataflow, simulation topology, and MQTT bridge interfaces.
- **Changelog (`docs/CHANGELOG.md`)**:
  - Consolidate all PRs into structured `Added`, `Changed`, `Fixed`, and `Security` sections under `[Unreleased]`.

### 4.2 User Documentation & Quickstart
- Comprehensive `README.md` updates:
  - Architecture diagram and capability overview.
  - Environment variable matrix (all flags, default values, and operational impacts).
  - Docker Compose home-lab deployment recipe.
  - Single-device Gaia pairing quickstart guide.

### 4.3 Versioned Release (`v1.0.0-rc1` / `v1.0.0`)
- Git tag creation and release notes generation.
- Automated release build with cross-compiled binaries (`linux/amd64`, `linux/arm64`) and multi-arch Docker image publishing.
