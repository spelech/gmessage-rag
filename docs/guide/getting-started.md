# Quick Start Guide

`gmessages-rag` is a self-contained Go daemon that pairs with Google Messages to ingest messages, run Vision OCR, group sessions, update Living Dossiers, and expose an MCP server.

## Prerequisites

- **Docker & Docker Compose** (Recommended for local orchestration).
- Alternatively: **Go 1.24+** and **PostgreSQL 17+** with the `pgvector` (>= 0.8.0) extension installed.
- An active Android phone with Google Messages installed.

## 1. Configure Environment

Copy the example environment file and customize your settings:

```bash
cp .env.example .env
```

Key environment settings include:

| Variable | Description | Default |
| :--- | :--- | :--- |
| `DATABASE_URL` | PostgreSQL connection string | `postgres://jules:postgres@localhost:5432/gmessages?sslmode=disable` |
| `HTTP_PORT` | HTTP daemon listening port | `8090` |
| `OPENAI_API_KEY` | OpenAI API key for OCR and Dossier reconciliation | `""` |
| `DEBOUNCER_IDLE_MINUTES` | Idle silence before triggering pipeline run | `30` |
| `ENABLE_READ_RECEIPT_HEURISTIC` | Inferred delivery receipts for SMS | `true` |

## 2. Launch Services

Start the daemon and PostgreSQL using Docker Compose:

```bash
docker compose up -d --build
```

Verify service health:

```bash
curl -f http://localhost:8090/healthz
```

Expected output:
```json
{"status":"healthy","database":"connected","version":"1.0.0"}
```
