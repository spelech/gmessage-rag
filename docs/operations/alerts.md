# Push Alerts & Monitoring (`AUTH-03`, `TRANS-04`, `OBS-01`)

`gmessages-rag` provides integrated health monitoring, Prometheus metrics, and push alerting.

## Push Alerting with Apprise & ntfy (`AUTH-03`)

When pairing expires or re-authentication is needed, the daemon dispatches push notifications via Apprise or ntfy:

```text
APPRISE_URLS="ntfy://ntfy.sh/my-secret-topic"
```

Notifications include direct links to `/pair` for one-tap mobile re-pairing.

## Connection Watchdog (`TRANS-04`)

To avoid alert fatigue caused by brief workplace cell signal drops:
- Watches `PhoneNotResponding` and `PhoneRespondingAgain` events.
- Enforces a configurable grace period (e.g. 15 minutes) before triggering disconnect alerts.
- Cancels pending notifications if connectivity restores within the window.

## Prometheus Metrics & Probes (`OBS-01`)

- **Metrics Endpoint (`/metrics`)**: Standard Prometheus metrics tracking ingested messages, media bytes, session chunks, and active connections.
- **Health Probe (`/healthz`)**: Verifies database pool responsiveness and daemon lifecycle state for container orchestrators.
