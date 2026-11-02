---
layout: home

hero:
  name: "gmessages-rag"
  text: "Real-Time Google Messages Ingestion & Living Dossier MCP Server"
  tagline: "Pairs directly with Google Messages (RCS/SMS), stores content-addressed media, extracts Vision OCR, reconciles Living Dossiers, and exposes an agent-native MCP server."
  actions:
    - theme: brand
      text: Get Started
      link: /guide/getting-started
    - theme: alt
      text: View Architecture
      link: /architecture/overview

features:
  - title: Matrix-Free RCS/SMS Pairing
    details: Pairs directly via Google Messages Web protocol using QR codes or single-device Gaia emoji matching without Matrix homeservers.
  - title: Content-Addressed Media & Vision OCR
    details: Automatically downloads, decrypts, and hashes RCS media with automated OpenAI-compatible vision model OCR extraction.
  - title: 2-Hour Windowing & Hybrid RAG
    details: Combines full-text search and pgvector halfvec(1536) cosine similarity using Reciprocal Rank Fusion (k=60).
  - title: Living Dossier State Engine
    details: 30-minute idle debouncing drives continuous LLM state reconciliation, temporal date anchoring, and Markdown dossier export.
  - title: Agent-Native MCP Server
    details: Implements the 2026-07-28 Model Context Protocol over Streamable HTTP, SSE, and stdio with prompt injection guardrails.
  - title: Diagnostics & Home Assistant
    details: Web management dashboard, Context-Cortex style test bench, Apprise push alerts, and Home Assistant REST/MQTT bridges.
---
