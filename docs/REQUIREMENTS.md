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

---

## Requirements and Traceability Matrix

| Requirement ID | Category | Type | Summary | Acceptance Criteria | Automated Test Proof |
| :--- | :--- | :--- | :--- | :--- | :--- |
| **`AUTH-01`** | `AUTH` | `PositiveFeature` | **Session Token Persistence** | Atomically writes `*libgm.AuthData` to `/data/auth/session.json` with `0600` permissions on `PairSuccessful` and `AuthTokenRefreshed`. | `internal/gm/pairing_test.go::TestSessionPersistence_Permissions` |
| **`AUTH-02`** | `AUTH` | `PositiveFeature` | **Dual Pairing Workflow** | Supports terminal QR printing (`gmessages-rag pair`) and web UI QR rendering (`GET /pair`) with live refresh. | `internal/gm/pairing_test.go::TestPairingHandlers` |
| **`DB-01`** | `DB` | `PositiveFeature` | **Schema Migration & Vector Init** | Automatically applies embedded `001_init.sql` creating `conversations`, `conversation_sessions` (with `halfvec(1536)` HNSW), `messages`, `media_attachments`, and `contact_dossiers`. | `internal/db/db_test.go::TestDatabaseMigrations` |
| **`DB-02`** | `DB` | `PositiveFeature` | **Message & Reaction Ingestion** | Normalizes microsecond/millisecond timestamps to UTC, resolves incoming/outgoing direction, and idempotently upserts messages and emoji reactions. | `internal/db/messages_test.go::TestUpsertMessages_Idempotent` |
| **`DB-03`** | `DB` | `PositiveFeature` | **2-Hour Session Windowing** | Groups messages separated by $\le 2\text{ hours}$ into `conversation_sessions` with absolute calendar headers and inline media OCR annotations. | `internal/db/sessions_test.go::TestSessionGrouping_2HourGap` |
| **`DB-04`** | `DB` | `PositiveFeature` | **Hybrid RRF Search Query** | Combines full-text BM25 (`ts_rank_cd`) and vector cosine similarity (`<=>`) using Reciprocal Rank Fusion ($k=60$) over session transcripts. | `internal/db/sessions_test.go::TestSearchSessionsHybrid_RRF` |
| **`DB-05`** | `DB` | `PositiveFeature` | **Living Dossier State & Audit History** | Upserts current structured state in `contact_dossiers` and snapshots prior state to `contact_dossier_history` for rollback safety. | `internal/db/dossiers_test.go::TestDossierVersionAuditHistory` |
| **`SEC-01`** | `SEC` | `FailClosedGuardrail` | **Content-Addressed Media Decryption** | Decrypts RCS attachments, computes SHA-256 hash, and stores files at `/data/media/<h1h2>/<h3h4>/<hash>.<ext>` with duplicate avoidance. | `internal/gm/media_test.go::TestMediaDownload_SHA256Pathing` |
| **`GUARD-01`** | `GUARD` | `FailClosedGuardrail` | **Prompt Injection XML Boundary** | All raw message texts, transcripts, and OCR text returned by MCP tools are wrapped in `<untrusted_conversation_data>` tags. | `internal/mcpserver/guardrails_test.go::TestWrapUntrustedData_Escaping` |
| **`GUARD-02`** | `GUARD` | `FailClosedGuardrail` | **Send-Message Restriction** | Deliberately omits outbound `send_message` MCP tool unless `ENABLE_MCP_SEND_MESSAGE=true` is explicitly configured. | `internal/mcpserver/tools_test.go::TestSendMessageTool_RegistrationGuard` |
| **`PIPE-01`** | `PIPE` | `PositiveFeature` | **Vision OCR Sidecar** | Extracts verbatim OCR text and 1-2 sentence visual description for image attachments in tracked conversations via OpenAI-compatible vision model. | `internal/pipeline/vision_test.go::TestVisionOCR_Extraction` |
| **`PIPE-02`** | `PIPE` | `PositiveFeature` | **Per-Conversation Idle Debouncer** | Maintains thread-safe timers per tracked conversation; triggers pipeline run after 30 minutes of messaging silence. | `internal/pipeline/debouncer_test.go::TestDebouncer_ResetOnNewMessage` |
| **`PIPE-03`** | `PIPE` | `PositiveFeature` | **LLM State Reconciliation Engine** | Reconciles new sessions against current dossier: anchors relative dates, applies chronological overrides, transitions past events, and exports Markdown to disk. | `internal/pipeline/dossier_test.go::TestDossierReconciliation_DateAnchoring` |
| **`MCP-01`** | `MCP` | `PositiveFeature` | **`list_conversations` Tool** | Lists conversations with IDs, contact names, participant identifiers, tracked status, and last activity timestamp. | `internal/mcpserver/tools_test.go::TestTool_ListConversations` |
| **`MCP-02`** | `MCP` | `PositiveFeature` | **`get_contact_dossier` Tool** | Returns current Markdown dossier, structured JSON state, plus live tail of unindexed messages sent after last dossier reconciliation. | `internal/mcpserver/tools_test.go::TestTool_GetContactDossier` |
| **`MCP-03`** | `MCP` | `PositiveFeature` | **`search_conversation_history` Tool** | Executes Hybrid RRF query across sessions with optional date bounds and returns formatted transcripts. | `internal/mcpserver/tools_test.go::TestTool_SearchConversationHistory` |
| **`MCP-04`** | `MCP` | `PositiveFeature` | **`get_messages_around_time` Tool** | Pulls verbatim message context and media OCR annotations within $\pm \text{window\_minutes}$ of a target timestamp. | `internal/mcpserver/tools_test.go::TestTool_GetMessagesAroundTime` |
| **`MCP-05`** | `MCP` | `PositiveFeature` | **`search_shared_media` Tool** | Searches media attachments by OCR text and visual descriptions. | `internal/mcpserver/tools_test.go::TestTool_SearchSharedMedia` |
| **`MCP-06`** | `MCP` | `PositiveFeature` | **`manage_tracked_contact` Tool** | Enables/disables thread tracking, initiates historical pagination backfill, or forces immediate dossier refresh. | `internal/mcpserver/tools_test.go::TestTool_ManageTrackedContact` |
| **`TRANS-01`** | `TRANS` | `PositiveFeature` | **Health Check Probe** | Exposes `/health` returning HTTP 200 with database connectivity status and version JSON. | `internal/web/server_test.go::TestHealthEndpoint_ReturnsHealthy` |
| **`TRANS-02`** | `TRANS` | `PositiveFeature` | **Streamable MCP & SSE Endpoints** | Exposes Streamable HTTP `/mcp` and Server-Sent Events `/sse` on port 8090. | `internal/web/server_test.go::TestMCPEndpoints_HTTPAndSSE` |
| **`TRANS-03`** | `TRANS` | `PositiveFeature` | **Briefing API & Outbound Webhook** | Exposes `/api/v1/briefing/today` for Home Assistant REST sensors and dispatches POST to `WEBHOOK_URL` when dossiers update. | `internal/web/webhook_test.go::TestWebhookDispatch_OnDossierUpdate` |

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
