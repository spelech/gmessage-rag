# System Architecture

`gmessages-rag` is built as an observable, high-performance Go daemon that synchronizes Google Messages in real time, organizes conversations into calendar-anchored chunks, and exposes them to AI agents via Model Context Protocol (MCP).

## Architectural Topology

```mermaid
flowchart TD
    subgraph GoogleMessages["Google Messages Cloud"]
        GMWeb["Google Messages Web Protocol"]
    end

    subgraph Daemon["gmessages-rag Daemon (Go 1.24+)"]
        subgraph Ingestion["gm (libgm Client)"]
            Pairing["Cookie Import Pairing<br>(CLI --cookies / Web GET+POST /pair)"]
            EventLoop["libgm Event Switch<br>(Incoming / Outgoing / Reactions)"]
            MediaHandler["Media Decryptor<br>(SHA-256 Storage)"]
            Backfill["Historical Pagination<br>(FetchMessages loop)"]
        end

        subgraph Pipeline["pipeline (Processing Engine)"]
            Debouncer["30m Per-Conversation<br>Idle Debouncer"]
            VisionOCR["Vision OCR Worker<br>(OpenAI-compatible)"]
            Sessionizer["2-Hour Window Chunker<br>& Transcript Formatter"]
            Embedder["Embedding Client<br>(halfvec 1536)"]
            DossierEngine["Living Dossier Engine<br>(LLM State Reconciliation)"]
        end

        subgraph Interfaces["Server Endpoints"]
            MCPServer["MCP Server<br>(/mcp, /sse, stdio)"]
            Guardrails["XML Guardrails<br>(untrusted_conversation_data)"]
            WebAPI["Web & REST Server<br>(/healthz, /pair, /api/v1/*)"]
            Webhook["Outbound Webhook<br>(POST to Home Assistant)"]
        end
    end

    subgraph Storage["Persistent Layer"]
        Postgres[("PostgreSQL 17 + pgvector<br>• conversations<br>• messages<br>• conversation_sessions<br>• media_attachments<br>• contact_dossiers & history")]
        DataAuth["/data/auth/session.json (0600)"]
        DataMedia["/data/media/ab/cd/<hash>.<ext>"]
        DataDossiers["/data/dossiers/<contact>.md"]
    end

    GMWeb <-->|Protobuf / WebSocket| Pairing & EventLoop & Backfill
    EventLoop -->|Decrypt Payload| MediaHandler
    MediaHandler --> DataMedia
    EventLoop -->|Upsert Raw| Postgres
    EventLoop -->|Activity Signal| Debouncer

    Debouncer -->|Trigger on Silence| VisionOCR
    VisionOCR -->|Update OCR| Postgres
    VisionOCR --> Sessionizer
    Sessionizer -->|Embed Transcript| Embedder
    Embedder -->|Upsert Sessions| Postgres
    Sessionizer --> DossierEngine
    DossierEngine -->|Update Current & Audit| Postgres
    DossierEngine -->|Write .md| DataDossiers
    DossierEngine -->|Notify| Webhook

    Pairing --> DataAuth
    Guardrails --> MCPServer
    Postgres <--> MCPServer
    Postgres <--> WebAPI
    Webhook --> DataDossiers
```

## Subsystem Responsibilities

1. **Ingestion Engine (`internal/gm`)**: Communicates with Google Messages Web WebSocket endpoints, handles pairing handshakes, and receives raw protobuf frames.
2. **Processing Pipeline (`internal/pipeline`)**: Debounces conversational bursts, manages 2-hour windowing, extracts OCR from images, and triggers LLM state updates.
3. **Database Layer (`internal/db`)**: Manages transactions, idempotency, HNSW indexing on vector embeddings, and full-text search dictionaries.
4. **Agent & Web Interfaces (`internal/mcpserver`, `internal/web`)**: Serves JSON-RPC 2.0 MCP requests, exposes diagnostic test benches, and dispatches home automation alerts.
