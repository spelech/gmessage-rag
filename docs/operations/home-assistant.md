# Home Assistant Integration (`TRANS-03`, `INT-01`)

Integrate `gmessages-rag` into your smart home for morning briefings, automations, and event dispatching.

## Morning Briefing API (`TRANS-03`)

Exposes a specialized REST endpoint designed for Home Assistant REST sensors:

```http
GET /api/v1/briefing/today
```

Returns today's appointments, upcoming commitments, and open loops extracted from Living Dossiers:

```json
{
  "date": "2026-03-29",
  "schedule": [
    {
      "time": "14:00",
      "contact": "Sarah",
      "summary": "Coffee meetup at Blue Bottle"
    }
  ],
  "open_loops": [
    {
      "contact": "Alex",
      "topic": "Send tax documents"
    }
  ]
}
```

## Outbound Webhooks

When a Living Dossier updates, the daemon posts an HTTP payload to `WEBHOOK_URL` allowing Home Assistant automations to trigger immediately.

## Bi-Directional MQTT Bridge (`INT-01`)

Connects directly to your Home Assistant MQTT broker:
- Publishes MQTT Discovery sensors for pairing state, unread counts, and connection health.
- Subscribes to `gmessages/commands/send` to allow smart home scripts to dispatch outbound SMS/RCS messages.
