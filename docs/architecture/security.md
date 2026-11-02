# Security & Storage Architecture

Data security and isolation are core design requirements across all storage and communication boundaries.

## Content-Addressed Media Storage (`SEC-01`, `SEC-02`)

All incoming and outgoing RCS media attachments (photos, videos, audio notes, PDFs) are decrypted in memory and stored using a content-addressed SHA-256 layout:

```text
/data/media/<sha256[0:2]>/<sha256[2:4]>/<sha256>.<ext>
```

- **File Permissions**: Files are created with strict POSIX `0600` permissions (read/write by owner only).
- **Directory Permissions**: Intermediate path directories use POSIX `0700` permissions.
- **De-duplication**: If the computed SHA-256 hash already exists on disk, duplicate file writes are skipped.
- **Full-Resolution Mode (`SEC-02`)**: When `PULL_FULL_SIZE_MEDIA=true` is enabled, the daemon calls `libgm.Client.GetFullSizeImage` to download uncompressed originals prior to vision OCR.

## Session Token Security (`AUTH-01`)

- Session auth credentials received during pairing are stored in `/data/auth/session.json`.
- Writes are performed atomically using a `.tmp` file and rename sequence.
- Permissions are strictly enforced to `0600` on creation and on subsequent token refreshes.

## Fail-Closed Prompt Injection Defense (`GUARD-01`, `GUARD-02`)

External text originating from SMS or RCS messages is untrusted. Malicious parties could attempt prompt injection attacks against LLMs consuming MCP tools.

To mitigate this:
1. **XML Boundary Encapsulation**: All conversational text, OCR output, and transcripts returned by MCP tools are automatically enclosed in `<untrusted_conversation_data>` tags.
2. **Special Character Escaping**: Any internal closing tags are escaped to prevent escaping the protective wrapper.
3. **Outbound Send Restriction**: The outbound message tool `send_message` is not registered unless `ENABLE_MCP_SEND_MESSAGE=true` is explicitly configured.
