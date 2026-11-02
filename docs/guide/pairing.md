# Pairing Workflows (`AUTH-01` - `AUTH-04`)

`gmessages-rag` supports multiple authentication mechanisms to connect with Google Messages without requiring physical cables or Matrix homeservers.

## Workflow A: Terminal QR Code (`AUTH-02`)

For command-line pairing:

```bash
docker exec -it gmessages-rag gmessages-rag pair
```

A QR code renders directly in your terminal:
1. Open **Google Messages** on your Android phone.
2. Tap your profile icon or three dots > **Device pairing**.
3. Tap **QR code scanner** and scan the terminal QR code.
4. Credentials are automatically saved to `/data/auth/session.json` with POSIX `0600` permissions.

## Workflow B: Web Management QR Code (`AUTH-02`)

Open the Web Dashboard pairing page in your browser:

```
http://localhost:8090/pair
```

- Features live auto-refreshing SVG/PNG QR codes.
- Automatically transitions upon scan confirmation.

## Workflow C: Single-Device Gaia Emoji Pairing (`AUTH-04`)

When pairing on a mobile phone without a second computer screen, camera QR scanning is impossible. `gmessages-rag` supports native Google Account (Gaia) pairing:

```mermaid
sequenceDiagram
    autonumber
    participant Daemon as gmessages-rag Daemon
    participant LibGM as libgm Gaia Engine
    participant Push as Apprise / ntfy
    participant Phone as User Phone
    participant GMApp as Google Messages Android App

    Daemon->>LibGM: StartGaiaPairing()
    LibGM-->>Daemon: Challenge Emoji (e.g. 🚀 Rocket)
    Daemon->>Push: Push Alert ("Challenge: Select 🚀")
    Push-->>Phone: Notification ("Pairing challenge: 🚀")
    Phone->>GMApp: Open Google Messages -> Device Pairing
    GMApp-->>Phone: Prompt ("Which emoji matches?")
    Phone->>GMApp: Tap 🚀
    GMApp->>LibGM: Ukey2 Handshake Completed
    LibGM-->>Daemon: PairSuccessful (session saved)
```

1. Navigate to `/pair` or trigger Gaia pairing.
2. The daemon requests a pairing challenge from Google.
3. The expected emoji (e.g. `🚀` or `🐶`) is displayed on the web interface or pushed via Apprise/ntfy.
4. Open Google Messages on the same phone and tap the matching emoji. **No camera scan required.**

## Gated Live Smoke Test (`HARN-04`)

Before relying on a real account in production, run the opt-in end-to-end smoke test. It proves the real provider handoff: pairing, session persistence, connection, and one inbound message.

It never runs in CI. It requires the `live` build tag plus explicit environment variables:

```bash
GM_ALLOW_NETWORK=1 \
LIVE_GM_TEST=1 \
SESSION_PATH=/tmp/live-smoke-session.json \
DATABASE_URL='postgres://user:pass@host:5432/gmessages' \
go test -tags live -race -v -timeout 5m -run TestLiveSmoke_EndToEnd ./internal/gm/
```

During the run:
1. The test prints the Gaia emoji challenge to the test log.
2. Approve the matching emoji in Google Messages on your phone.
3. Send one test message from the paired phone within the 2-minute window.
4. The test asserts the session file is `0600`, `Connect` succeeds, and the inbound message reaches the ingestion callback.

Use a dedicated `SESSION_PATH` so the smoke test never clobbers the daemon's session. Do not commit the session file or any message content.
