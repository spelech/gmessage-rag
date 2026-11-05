# MCP Tools Reference (`MCP-01` - `MCP-07`)

`gmessages-rag` registers 6 core MCP tools to allow agents to interact safely with messages, media, and dossiers. When `ENABLE_MCP_SEND_MESSAGE=true` and a paired sender is wired, it also registers the optional `send_message` tool.

## 1. `list_conversations` (`MCP-01`)
Lists all ingested conversations with metadata and activity timestamps.
- **Inputs**: None.
- **Output**: JSON array of conversations with `id`, `name`, `is_group`, `is_tracked`, and `last_message_at`.

## 2. `get_contact_dossier` (`MCP-02`)
Retrieves the Living Dossier for a contact along with the unindexed message tail.
- **Inputs**: `contact_name` (string) or `conversation_id` (string).
- **Output**: Formatted Markdown dossier, structured state JSON, and live tail of unindexed messages.

## 3. `search_conversation_history` (`MCP-03`)
Executes a Hybrid RRF search query across session transcripts.
- **Inputs**: `query` (string), optional `start_date` and `end_date`, `limit` (default 5).
- **Output**: Ranked session transcripts with date headers and summary text.

## 4. `get_messages_around_time` (`MCP-04`)
Extracts verbatim message context surrounding a specific point in time.
- **Inputs**: `target_time` (ISO-8601 string), `window_minutes` (integer, default 30).
- **Output**: Chronological message transcript including sender IDs and inline media notes.

## 5. `search_shared_media` (`MCP-05`)
Searches media attachments using vision OCR text and descriptions.
- **Inputs**: `query` (string), `conversation_id` (optional).
- **Output**: List of matching media attachments with OCR text, file size, and paths.

## 6. `manage_tracked_contact` (`MCP-06`)
Enables tracking, disables tracking, or triggers immediate backfill.
- **Inputs**: `conversation_id` (string), `action` (`"track"`, `"untrack"`, `"backfill"`, `"refresh_dossier"`).
- **Output**: Confirmation status and triggered action result.

## 7. `send_message` (`MCP-07`, optional)
Sends a text message, a base64 media attachment, or an emoji reaction into an existing conversation. This tool is only registered when `ENABLE_MCP_SEND_MESSAGE=true` and a paired `gm.Client` sender is wired; otherwise it is absent and requests fail closed.
- **Inputs**: `conversation_id` (string, required), plus exactly one of `text`, `media_base64`, or `reaction_message_id`.
  - Text: `text` (string), optional `reply_to_message_id` (string).
  - Media: `media_base64` (string), optional `media_file_name`, `media_mime_type`, `caption`, `reply_to_message_id`. Decoded media is capped at 10 MiB.
  - Reaction: `reaction_message_id` (string), `reaction_emoji` (string), optional `reaction_remove` (bool).
- **Output**: For text/media, the `tmp_id`, `conversation_id`, `sent_at`, and `status`. For reactions, the persisted `message_id`, `emoji`, and `action` (`added`/`removed`).
- **Persistence**: Reaction changes are persisted through `db.AddReaction`/`db.RemoveReaction`. Text and media persistence relies on the outgoing event echo, which the connect-on-send path holds the connection open briefly to receive.
