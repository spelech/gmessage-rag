# Web Management Dashboard (`UI-01`)

`gmessages-rag` provides an embedded single-page application (SPA) hosted directly on port 8090.

## Key Features

- **Daemon Status**: Live indicators for Google Messages connection state, token expiration timers, and uptime.
- **Cookie Import Pairing Console**: Text area for pasting a signed-in browser cURL command or cookie header, live validation feedback, and the Gaia emoji challenge.
- **Tracked Contacts Roster**: Toggle tracking status on individual threads, start historical backfills, or trigger instant dossier re-indexing.
- **Database Metrics**: Live statistics on stored messages, conversation sessions, media files, and embeddings.

## Accessing the Dashboard

Open your web browser to:

```
http://localhost:8090/
```

Or for direct pairing:

```
http://localhost:8090/pair
```
