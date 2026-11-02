# System Architecture: gmessages-rag

`gmessages-rag` is a self-contained Go daemon that pairs directly with Google Messages (RCS/SMS), stores messages in PostgreSQL 17 + `pgvector`, runs Vision OCR and 2-hour session chunking, maintains self-updating Living Dossiers per tracked contact, and exposes an agent-native Model Context Protocol (MCP) server alongside Home Assistant REST/webhook APIs.

---

## Architectural Topology

```mermaid
flowchart TD
    subgraph GoogleMessages["Google Messages Cloud"]
        GMWeb["Google Messages Web Protocol"]
    end

    subgraph Daemon["gmessages-rag Daemon (Go 1.24+)"]
        subgraph Ingestion["gm (libgm Client)"]
            Pairing["Pairing Handler<br>(CLI QR / Web GET /pair)"]
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
            WebAPI["Web & REST Server<br>(/health, /pair, /api/v1/*)"]
            Webhook["Outbound Webhook<br>(POST to Home Assistant)"]
        end
    end

    subgraph Storage["Persistent Layer"]
        Postgres[("PostgreSQL 17 + pgvector<br>• conversations<br>• messages<br>• conversation_sessions (HNSW halfvec)<br>• media_attachments<br>• contact_dossiers & history")]
        DataAuth["/data/auth/session.json (0600)"]
        DataMedia["/data/media/ab/cd/<hash>.<ext>"]
        DataDossiers["/data/dossiers/<contact>.md"]
    end

    subgraph Consumers["Agent & Smart Home Ecosystem"]
        Agents["AI Agents<br>(OpenCode, Antigravity, Claude Code)"]
        HomeAssistant["Home Assistant / NetDaemon<br>(Morning Briefing REST / Webhook)"]
        Obsidian["Obsidian / Local Notes<br>(Direct .md Reader)"]
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
    Webhook --> HomeAssistant
    WebAPI <--> HomeAssistant
    MCPServer <--> Agents
    DataDossiers --> Obsidian
```

---

## Data Model and Schema Architecture

```mermaid
erDiagram
    conversations ||--o{ messages : "contains"
    conversations ||--o{ conversation_sessions : "grouped into"
    conversations ||--o{ media_attachments : "receives"
    conversations ||--o| contact_dossiers : "has current state"
    conversations ||--o{ contact_dossier_history : "records audits"
    conversation_sessions ||--o{ messages : "indexes"
    messages ||--o{ media_attachments : "attaches"

    conversations {
        text id PK
        text name
        jsonb participants
        boolean is_group
        boolean is_tracked
        timestamptz last_message_at
        text backfill_cursor
        boolean backfill_complete
        jsonb metadata
    }

    messages {
        text id PK
        text conversation_id FK
        bigint session_id FK
        text sender_id
        text sender_name
        boolean is_from_me
        timestamptz sent_at
        text body
        text reply_to_message_id
        jsonb reactions
        int raw_status
        boolean is_backfill
    }

    conversation_sessions {
        bigserial id PK
        text conversation_id FK
        timestamptz started_at
        timestamptz ended_at
        int message_count
        text summary
        text transcript
        tsvector search_tsv
        halfvec embedding
        boolean is_processed
    }

    media_attachments {
        bigserial id PK
        text message_id FK
        text conversation_id FK
        text sha256_hash
        text relative_path
        text mime_type
        bigint file_size_bytes
        int width
        int height
        text ocr_text
        text visual_description
        boolean vision_processed
    }

    contact_dossiers {
        text conversation_id PK,FK
        text contact_name
        text markdown_content
        jsonb structured_state
        timestamptz last_processed_message_at
        int version
        timestamptz updated_at
    }

    contact_dossier_history {
        bigserial id PK
        text conversation_id FK
        int version
        text markdown_content
        jsonb structured_state
        bigint trigger_session_id FK
        timestamptz created_at
    }
```

---

## Real-Time Ingestion and Pipeline Lifecycle

```mermaid
sequenceDiagram
    autonumber
    actor Sarah as Tracked Contact (Sarah)
    participant GM as Google Messages Cloud
    participant Daemon as gmessages-rag (libgm)
    participant DB as PostgreSQL 17 + pgvector
    participant Media as /data/media Disk
    participant Debounce as Idle Debouncer (30m)
    participant Pipeline as Vision & Dossier Pipeline
    participant LLM as Vision / Chat LLM
    participant Agent as AI Agent (MCP)

    Sarah->>GM: Sends text + calendar screenshot
    GM->>Daemon: libgm WrappedMessage event
    Daemon->>Media: Decrypt & write SHA-256 image file
    Daemon->>DB: Upsert message & media_attachment
    Daemon->>Debounce: Reset conversation timer to 30m
    Note over Debounce: 30 minutes pass without new messages
    Debounce->>Pipeline: Trigger pipeline run
    Pipeline->>DB: Query unprocessed media
    Pipeline->>LLM: Run Vision OCR & Description
    LLM-->>Pipeline: Return OCR text & visual description
    Pipeline->>DB: Update media_attachments
    Pipeline->>DB: Query unassigned messages
    Pipeline->>Pipeline: Group into 2h sessions & render calendar-anchored transcript
    Pipeline->>LLM: Generate summary & vector embedding
    Pipeline->>DB: Upsert conversation_sessions (TSV + halfvec)
    Pipeline->>DB: Fetch current contact_dossier
    Pipeline->>LLM: Run Dossier State Reconciliation
    LLM-->>Pipeline: Return updated structured_state & markdown
    Pipeline->>DB: Snapshot prior state to history & update dossier
    Pipeline->>Pipeline: Write /data/dossiers/sarah.md

    Agent->>Daemon: Call MCP get_contact_dossier("Sarah")
    Daemon->>DB: Read dossier + live tail of raw messages
    Daemon-->>Agent: Return XML-wrapped dossier & live updates
```

---

## Directory and Subsystem Layout

```text
gmessages-rag/
├── cmd/
│   └── gmessages-rag/
│       └── main.go                 # CLI entrypoint (pair, serve, backfill, status)
├── internal/
│   ├── config/
│   │   └── config.go               # Env var & configuration loader
│   ├── db/
│   │   ├── migrations/
│   │   │   └── 001_init.sql        # Embedded pgvector & schema migrations
│   │   ├── db.go                   # pgxpool connection & migration runner
│   │   ├── messages.go             # Upsert messages, conversations, reactions
│   │   ├── sessions.go             # 2-hour session grouping & Hybrid RRF queries
│   │   └── dossiers.go             # Living dossier CRUD & snapshot versioning
│   ├── gm/
│   │   ├── client.go               # libgm wrapper, event switch, reconnect loop
│   │   ├── pairing.go              # QR terminal + Web UI QR & Gaia pairing handlers
│   │   ├── media.go                # Media download, decryption & SHA-256 storage
│   │   └── backfill.go             # Historical pagination via client.FetchMessages
│   ├── pipeline/
│   │   ├── debouncer.go            # Per-conversation idle timer (e.g., 30m)
│   │   ├── sessionizer.go          # Groups unindexed messages into 2h sessions
│   │   ├── vision.go               # Multimodal OCR + image description worker
│   │   ├── embedder.go             # OpenAI-compatible batch embedding client
│   │   ├── dossier.go              # LLM state-reconciliation engine + Markdown writer
│   │   └── prompts.go              # System prompts for Vision, Session Summary & Dossier
│   ├── mcpserver/
│   │   ├── server.go               # Streamable HTTP / SSE MCP server setup
│   │   ├── tools.go                # 6 core MCP tool handlers
│   │   └── guardrails.go           # XML boundary wrapping for untrusted text
│   └── web/
│       ├── server.go               # Lightweight HTTP server (/health, /pair, /api/*)
│       └── webhook.go              # Outbound webhook notifier (Home Assistant / MQTT)
├── data/                           # Local bind-mount directory (git-ignored)
│   ├── auth/                       # Holds session.json (0600 permissions)
│   ├── media/                      # Content-addressed decrypted attachments
│   └── dossiers/                   # Auto-exported Markdown files (<slug>.md)
├── docs/                           # Living Documentation
│   ├── ARCHITECTURE.md             # System topology and sequence diagrams
│   ├── REQUIREMENTS.md             # SRS with traceability matrix
│   └── CHANGELOG.md                # SemVer change history
├── scripts/                        # Git Flow and operational scripts
│   └── commit.sh                   # Conventional Commit and branch validator
├── .github/                        # CI/CD and automation
│   ├── dependabot.yml              # Dependency update configuration
│   └── workflows/
│       └── ci.yml                  # 4-stage CI pipeline
├── Dockerfile                      # Multi-stage Alpine container
├── docker-compose.yml              # Local orchestration with pgvector
├── Makefile                        # Local build and test targets
└── README.md                       # Project overview and quick start
```
