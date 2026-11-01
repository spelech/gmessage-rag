<img src="assets/icon.svg" width="64" height="64" alt="gmessages-rag">

# gmessages-rag

[![License: AGPL-3.0](https://img.shields.io/badge/License-AGPL--3.0-blue.svg)](LICENSE)
[![Go Version](https://img.shields.io/badge/Go-1.24+-00ADD8.svg)](https://go.dev/)
[![PostgreSQL](https://img.shields.io/badge/PostgreSQL-17_%2B_pgvector-336791.svg)](https://github.com/pgvector/pgvector)
[![MCP Protocol](https://img.shields.io/badge/MCP-Streamable_HTTP_%2F_SSE-purple.svg)](https://modelcontextprotocol.io/)

> **Real-Time Google Messages (RCS/SMS) Ingestion, Vision OCR, Living Dossier & Hybrid RAG MCP Server**

`gmessages-rag` pairs directly with Google Messages via `go.mau.fi/mautrix-gmessages/pkg/libgm` (without needing Matrix homeservers or phone root hacks). It ingests texts, reactions, and RCS media, groups them into contiguous 2-hour conversation sessions, runs multimodal Vision OCR on shared images, continuously reconciles a self-updating "Living Dossier" state engine per tracked contact, and exposes an agent-native Model Context Protocol (MCP) server alongside Home Assistant REST/webhook APIs.

This repository adheres to Steven T. Pelech's **[AgenticEngineeringToolbelt](https://github.com/spelech/AgenticEngineeringToolbelt)** standards, Git Flow discipline, anti-test-theatre principles, and living documentation architecture.

---

## Key Capabilities

1. **Matrix-Free Real-Time RCS/SMS Sync:**
   - Pairs via QR code in terminal (`gmessages-rag pair`) or web UI (`GET /pair`).
   - Ingests incoming/outgoing messages, emoji reactions, and reply chains.
   - Paginates historical message backfill (`client.FetchMessages`) up to 5,000 messages.
2. **Content-Addressed Media & Vision OCR Pipeline:**
   - Decrypts attachments to `/data/media/<sha256[0:2]>/<sha256[2:4]>/<sha256>.<ext>`.
   - Runs vision OCR and visual description extraction via OpenAI-compatible endpoints.
3. **2-Hour Session Windowing & Hybrid RAG:**
   - Groups contiguous messages ($\le 2\text{ hours}$ silence) into `conversation_sessions`.
   - Generates calendar-anchored, timezone-aware transcripts with inline media OCR.
   - Fuses PostgreSQL full-text search (`ts_rank_cd`) and `halfvec(1536)` vector cosine distance (`<=>`) using Reciprocal Rank Fusion (RRF, $k=60$).
4. **Self-Updating "Living Dossier" State Engine:**
   - 30-minute per-conversation idle debouncer triggers LLM state reconciliation.
   - Resolves relative temporal statements (*"next Tuesday"* $\rightarrow$ explicit date), maintains chronological overrides, transitions past events, and writes Markdown dossiers to `/data/dossiers/<contact>.md`.
   - Audits all dossier mutations into `contact_dossier_history`.
5. **Agent-Native MCP Server with Prompt Injection Guardrails:**
   - Exposes Streamable HTTP (`/mcp`), Server-Sent Events (`/sse`), and stdio transports.
   - Provides 6 core MCP tools (`list_conversations`, `get_contact_dossier`, `search_conversation_history`, `get_messages_around_time`, `search_shared_media`, `manage_tracked_contact`).
   - Wraps all raw external SMS/RCS and OCR data in explicit `<untrusted_conversation_data>` XML guardrail boundaries.
6. **Smart Home & Home Assistant Integration:**
   - Exposes `/api/v1/briefing/today` returning today/tomorrow's schedule and open loops for morning briefings.
   - Dispatches outbound POST webhooks to Home Assistant on dossier updates.

---

## Quick Start

### 1. Prerequisites
- Docker & Docker Compose
- Or Go 1.24+ and a running PostgreSQL 17 instance with `pgvector`

### 2. Configure Environment
```bash
cp .env.example .env
# Edit .env with your LLM API keys and configuration
```

### 3. Start Database and Daemon
```bash
docker compose up -d --build
```

### 4. Pair with Google Messages
```bash
# Option A: Pair via terminal QR code
docker exec -it gmessages-rag gmessages-rag pair

# Option B: Pair via web browser
# Open http://localhost:8090/pair in your browser
```

---

## Architecture & Living Documentation

- [**`docs/ARCHITECTURE.md`**](docs/ARCHITECTURE.md): Architectural topology, ER diagram, sequence flow, and subsystem layout.
- [**`docs/REQUIREMENTS.md`**](docs/REQUIREMENTS.md): Software Requirements Specification (SRS) with requirement category taxonomy (`AUTH`, `DB`, `GUARD`, `MCP`, `SEC`, `TRANS`, `PIPE`) and test traceability matrix.
- [**`docs/CHANGELOG.md`**](docs/CHANGELOG.md): SemVer release and change notes.
- [**`AGENTS.md`**](AGENTS.md) / [**`GEMINI.md`**](GEMINI.md): Agent engineering guidelines and rules.
