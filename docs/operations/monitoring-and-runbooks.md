# Operations, Monitoring & Runbooks

This guide covers production operations, Prometheus metrics monitoring, push alert configuration, and disaster recovery runbooks.

---

## 1. Prometheus Metrics (`OBS-01`)

The daemon exports real-time Prometheus telemetry at `GET /metrics`:

### Core Metrics Catalog
| Metric Name | Type | Labels | Description |
| :--- | :--- | :--- | :--- |
| `gmessages_messages_ingested_total` | Counter | `protocol` (rcs, sms), `direction` (inbound, outbound) | Total messages ingested |
| `gmessages_media_decrypted_total` | Counter | `status` (success, failure) | Media decryption events |
| `gmessages_active_websocket_connections` | Gauge | - | Active long polling sessions |
| `gmessages_backfill_duration_seconds` | Histogram | `conversation_id` | Latency distribution for backfills |
| `gmessages_debouncer_queue_depth` | Gauge | - | Active flush timers currently queued |

### Prometheus Scrape Config
```yaml
scrape_configs:
  - job_name: 'gmessages-rag'
    scrape_interval: 15s
    static_configs:
      - targets: ['gmessages-rag:8090']
```

---

## 2. Health & Readiness Probes

Lightweight HTTP probes are exposed for Kubernetes and Docker Compose health checks:
- **`GET /healthz/live`**: Returns `200 OK` as long as the HTTP listener is operational.
- **`GET /healthz/ready`**: Verifies database pool reachability (`db.Ping(ctx)`) and Google Messages long-polling connectivity. Returns `503 Service Unavailable` if database or connection is lost.

---

## 3. Multi-Channel Push Alerting (`NOTIF-01`, `NOTIF-02`)

To notify operators when phone pairing is lost or requires re-authentication, `gmessages-rag` integrates with Apprise and ntfy:

### 15-Minute Connection Watchdog (`NOTIF-02`)
Cell phones frequently drop Wi-Fi or cellular connections temporarily (e.g. entering an elevator or subway). The built-in `Watchdog` buffers disconnections for **15 minutes** before triggering an alert:
- If the phone reconnects within 15 minutes, the timer is canceled silently without alert spam.
- If the disconnect exceeds 15 minutes, a push alert is dispatched containing direct action URLs to the pairing interface.

### Configuration
```bash
# Apprise: Supports 80+ notification services
APPRISE_URL="discord://webhook_id/webhook_token,tgram://bot_token/chat_id"

# ntfy: Lightweight pub/sub push notifications
NTFY_URL="https://ntfy.sh/my-secret-gmessages-topic"

# Notification options
NOTIFY_ON_PHONE_DISCONNECT=true
PHONE_DISCONNECT_GRACE_MINUTES=15
EXTERNAL_WEB_URL="https://messages.home.internal"
```

---

## 4. Operational Runbooks

### Runbook 1: Re-Authenticating a De-Paired Session
1. **Symptom**: Prometheus gauge `gmessages_active_websocket_connections` drops to 0; ntfy notification received: *"Google Messages session requires re-authentication"*.
2. **Resolution**:
   - Navigate to `EXTERNAL_WEB_URL/pair` to scan the auto-refreshing QR code with your phone camera, OR:
   - Run `docker exec -it gmessages-rag gmessages-rag pair --gaia` to approve via single-device emoji challenge.
3. **Verification**: Confirm `/healthz/ready` returns `200 OK`.

### Runbook 2: Manual Contact Backfill Trigger
1. To force a backfill for a conversation:
   ```bash
   curl -X POST http://localhost:8090/api/v1/contacts/track \
     -H "Content-Type: application/json" \
     -d '{"conversation_id": "conv-target-id", "is_tracked": true}'
   ```
2. Monitor progress via Prometheus metric `gmessages_backfill_duration_seconds`.
