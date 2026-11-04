# Pairing Workflows (`AUTH-01` - `AUTH-04`)

`gmessages-rag` pairs with Google Messages by importing Google session cookies. The Google Messages app no longer offers camera-based linking. Cookie import works like the Matrix token login: copy a signed-in request from your browser, then paste it into the daemon.

## Required Cookies (`AUTH-02`)

The importer requires these Google session cookies:

- `SID`
- `HSID`
- `OSID`
- `SSID`
- `APISID`
- `SAPISID`

The importer preserves extra cookies. It validates the required set and never prints cookie values in errors.

## Accepted Input Formats (`AUTH-02`)

Paste any of these formats:

- A cURL command copied from browser DevTools.
- A raw `Cookie: ...` header line.
- A flat JSON object such as `{"SID":"...","HSID":"..."}`.
- A wrapped JSON object such as `{"cookies":{"SID":"..."}}`.
- A JSON array from a cookie export tool such as `[{"name":"SID","value":"..."}]`.
- Semicolon-separated pairs such as `SID=...; HSID=...`.

## Workflow A: Import Cookies via CLI (`AUTH-02`)

1. Sign in to <https://messages.google.com/web> in your browser.
2. Open DevTools (`F12`) and select the **Network** tab.
3. Select a request to `messages.google.com`, for example `config`.
4. Right-click the request, select **Copy**, then select **Copy as cURL**.
5. Run the pairing command and paste the command when prompted:

```bash
docker exec -it gmessages-rag gmessages-rag pair
```

You can also read cookies from a file or stdin:

```bash
gmessages-rag pair --cookies /tmp/google-cookies.txt
cat /tmp/google-cookies.txt | gmessages-rag pair --cookies -
```

The daemon validates the cookies, saves the session to `/data/auth/session.json` with POSIX `0600` permissions, and starts the Gaia emoji challenge.

## Workflow B: Import Cookies via Web Dashboard (`AUTH-02`)

Open the dashboard pairing page:

```text
http://localhost:8090/pair
```

1. Follow the on-screen instructions to copy a cURL command from DevTools.
2. Paste the command into the text area.
3. Select **Pair with cookies**.
4. The page polls for status and shows the Gaia emoji challenge when Google accepts the cookies.

## Workflow C: Gaia Emoji Challenge (`AUTH-04`)

After cookie validation, Google requires a single-device device-link challenge. This step works on one phone without a second camera screen:

```mermaid
sequenceDiagram
    autonumber
    participant Daemon as gmessages-rag Daemon
    participant LibGM as libgm Gaia Engine
    participant Push as Apprise / ntfy
    participant Phone as User Phone
    participant GMApp as Google Messages Android App

    Daemon->>LibGM: PairWithCookies(cookies)
    LibGM-->>Daemon: Challenge Emoji (e.g. 🚀 Rocket)
    Daemon->>Push: Push Alert ("Challenge: Select 🚀")
    Push-->>Phone: Notification ("Pairing challenge: 🚀")
    Phone->>GMApp: Open Google Messages -> Device Pairing
    GMApp-->>Phone: Prompt ("Which emoji matches?")
    Phone->>GMApp: Tap 🚀
    GMApp->>LibGM: Ukey2 Handshake Completed
    LibGM-->>Daemon: PairSuccessful (session saved)
```

1. Open `/pair` or run the CLI command.
2. The daemon validates the imported cookies and requests a pairing challenge from Google.
3. The expected emoji (e.g. `🚀` or `🐶`) appears in the web interface or arrives through Apprise/ntfy.
4. Open Google Messages on the same phone and tap the matching emoji. No camera scan is required.

## Gated Live Smoke Test (`HARN-04`)

Before you rely on a real account in production, run the opt-in end-to-end smoke test. It proves the real provider handoff: cookie authentication, session persistence, connection, and one inbound message.

It never runs in CI. It requires the `live` build tag plus explicit environment variables:

```bash
GM_ALLOW_NETWORK=1 \
LIVE_GM_TEST=1 \
COOKIES_PATH=/tmp/google-cookies.txt \
SESSION_PATH=/tmp/live-smoke-session.json \
DATABASE_URL='postgres://user:pass@host:5432/gmessages' \
go test -tags live -race -v -timeout 5m -run TestLiveSmoke_EndToEnd ./internal/gm/
```

Set `COOKIES` inline instead of `COOKIES_PATH` when you cannot write a file. A pre-paired `SESSION_PATH` skips the cookie import.

During the run:
1. The test prints the Gaia emoji challenge to the test log when the session is not paired.
2. Approve the matching emoji in Google Messages on your phone.
3. Send one test message from the paired phone within the 2-minute window.
4. The test asserts the session file is `0600`, `Connect` succeeds, and the inbound message reaches the ingestion callback.

Use a dedicated `SESSION_PATH` so the smoke test never clobbers the daemon's session. Do not commit the session file or any message content.
