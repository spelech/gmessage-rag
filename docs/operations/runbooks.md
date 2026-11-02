# Operational Runbooks

Standard operational procedures for deploying, maintaining, and troubleshooting `gmessages-rag`.

## Runbook 1: Initial Deployment

1. Configure environment variables in `.env`.
2. Start containers: `docker compose up -d --build`.
3. Verify database initialization via `docker logs gmessages-rag`.
4. Navigate to `http://<host>:8090/pair` to authenticate with Google Messages.

## Runbook 2: Session Re-Authentication

When tokens expire or a device is unlinked:
1. An alert is sent via configured Apprise/ntfy endpoints.
2. Tap the link in the notification or open `http://<host>:8090/pair`.
3. Scan the new QR code or approve the Gaia emoji challenge on your phone.
4. The daemon resumes synchronization immediately without data loss.

## Runbook 3: Backfilling Conversation History

To backfill historical messages for a specific conversation:
1. Open the Web Dashboard or invoke the MCP tool `manage_tracked_contact`.
2. Specify the `conversation_id` and action `"backfill"`.
3. The daemon paginates via `libgm.FetchMessages` up to the configured batch limit.
4. Messages and media are deduplicated and indexed transactionally.
