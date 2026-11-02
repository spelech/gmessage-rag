# Diagnostic Test Bench (`UI-02`)

Modeled after the Context-Cortex control console, the diagnostic test bench enables interactive evaluation of daemon subsystems.

## Test Bench Capabilities

1. **Interactive MCP Playground**: Execute any of the 6 registered MCP tools with JSON input parameters and inspect formatted outputs and timings.
2. **Guardrail Visualizer**: Verifies that `<untrusted_conversation_data>` tags correctly encapsulate untrusted text and cannot be bypassed.
3. **Hybrid RRF Search Workbench**: Test queries against the ingested corpus with a side-by-side view of BM25 text rank vs cosine vector distance.
4. **Dossier Audit Diff Viewer**: Compare snapshot versions in `contact_dossier_history` to review how a contact profile evolved over time.

## Test Bench API Endpoints

The test bench communicates via `/api/v1/testbench/*`:

- `POST /api/v1/testbench/mcp`: Dispatches ad-hoc MCP tool invocations.
- `POST /api/v1/testbench/guardrail`: Tests XML boundary escaping on arbitrary input.
- `POST /api/v1/testbench/search`: Executes hybrid queries with debug scoring details.
