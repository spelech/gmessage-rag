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
3. Import fresh Google session cookies and approve the Gaia emoji challenge on your phone.
4. The daemon resumes synchronization immediately without data loss.

## Runbook 3: Backfilling Conversation History

To backfill historical messages for a specific conversation:
1. Open the Web Dashboard or invoke the MCP tool `manage_tracked_contact`.
2. Specify the `conversation_id` and action `"backfill"`.
3. The daemon paginates via `libgm.FetchMessages` up to the configured batch limit.
4. Messages and media are deduplicated and indexed transactionally.

## Runbook 4: Live End-to-End Smoke Test (`HARN-04`)

Before trusting a real account in production, validate the full provider handoff once.

Prerequisites:
- A dedicated session file (never the daemon's `SESSION_PATH`).
- A reachable PostgreSQL `DATABASE_URL`.
- Outbound network access to Google Messages.
- The paired phone in hand to approve the Gaia emoji and send a test message.

Procedure:
1. Run the gated test:
   ```bash
   GM_ALLOW_NETWORK=1 LIVE_GM_TEST=1 \
     SESSION_PATH=/tmp/live-smoke-session.json \
     DATABASE_URL='postgres://...' \
     go test -tags live -race -v -timeout 5m -run TestLiveSmoke_EndToEnd ./internal/gm/
   ```
2. Approve the emoji printed in the test log on your phone.
3. Send one message from the paired phone within the 2-minute window.
4. Confirm the test logs `Live smoke test completed` and exits `PASS`.
5. Inspect the database for the ingested message, then delete the temporary session file.

Failure triage:
- `timed out waiting for an inbound message` -> send a message during the window, or confirm the phone is still paired.
- `Gaia pairing should complete after operator approval` -> re-run and approve the emoji promptly; check for revoked sessions.
- `session file must be 0600` -> the session path is on a filesystem that ignores POSIX modes; use a local path.

## Runbook 5: Running DB Integration Tests Locally (`DB-07`)

The `internal/db` suite (sessions, RRF/HNSW, FTS, dossiers, receipts) skips itself unless a Postgres+pgvector database is reachable. To run it locally:

1. Start an ephemeral pgvector instance:
   ```bash
   docker run -d --name gmtest-db -p 5432:5432 \
     -e POSTGRES_USER=rcsrag -e POSTGRES_PASSWORD=changeme -e POSTGRES_DB=gmessages_test \
     pgvector/pgvector:pg17
   ```
2. Wait for readiness, then run the integration target:
   ```bash
   export DATABASE_URL='postgres://rcsrag:changeme@localhost:5432/gmessages_test?sslmode=disable'
   make test-integration
   ```
3. Expected: `internal/db` coverage ≥ 75% and total suite coverage ≥ 75%.
4. Tear down: `docker rm -f gmtest-db`.

CI runs the same suite against a `services.postgres` container in Stage 2, so a SQL regression fails the build.
