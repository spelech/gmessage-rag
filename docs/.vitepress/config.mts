import { defineConfig } from "vitepress";
import { withMermaid } from "vitepress-plugin-mermaid";
import mathjax3 from "markdown-it-mathjax3";

export default withMermaid(
  defineConfig({
    base: "/gmessage-rag/",
    title: "gmessages-rag",
    description:
      "Real-time Google Messages ingestion, content-addressed media storage, Living Dossiers, agent-native MCP server, and home automation alerting",

    themeConfig: {
      aside: true,
      outline: [2, 3],

      nav: [
        { text: "Guide", link: "/guide/getting-started" },
        { text: "Architecture", link: "/architecture/overview" },
        { text: "Specifications", link: "/specifications/requirements" },
        { text: "Operations", link: "/operations/monitoring-and-runbooks" },
      ],

      sidebar: {
        "/guide/": [
          {
            text: "Getting Started",
            items: [
              { text: "Quick Start", link: "/guide/getting-started" },
              { text: "Pairing Workflows", link: "/guide/pairing" },
              { text: "Configuration & Environment", link: "/guide/configuration" },
            ],
          },
          {
            text: "Management & Pipelines",
            items: [
              { text: "Web Dashboard & Diagnostics", link: "/guide/ui-and-diagnostics" },
              { text: "Pipelines & Ingestion", link: "/guide/pipelines-and-ingestion" },
            ],
          },
        ],
        "/architecture/": [
          {
            text: "Architecture & Design",
            items: [
              { text: "System Overview & ServeMux", link: "/architecture/overview" },
              { text: "Data Model & Schema", link: "/architecture/data-model" },
              { text: "Security & Guardrails", link: "/architecture/security" },
            ],
          },
        ],
        "/pipelines/": [
          {
            text: "Pipelines & Engines",
            items: [
              { text: "Pipelines & Ingestion Guide", link: "/guide/pipelines-and-ingestion" },
              { text: "ETL & Ingestion Details", link: "/pipelines/ingestion" },
              { text: "Vision OCR Pipeline", link: "/pipelines/vision-ocr" },
              { text: "Living Dossier Engine", link: "/pipelines/living-dossier" },
              { text: "Hybrid RAG & Vector Search", link: "/pipelines/hybrid-rag" },
            ],
          },
        ],
        "/mcp/": [
          {
            text: "Model Context Protocol",
            items: [
              { text: "MCP Specification (2026-07-28)", link: "/specs/mcp-server-spec" },
              { text: "Tools Reference", link: "/mcp/tools" },
              { text: "Prompt Injection Defense", link: "/mcp/guardrails" },
            ],
          },
        ],
        "/specs/": [
          {
            text: "Specifications",
            items: [
              { text: "Living Dossiers & Hybrid RAG", link: "/specs/dossiers-and-hybrid-rag" },
              { text: "MCP Server Specification (2026-07-28)", link: "/specs/mcp-server-spec" },
              { text: "Requirements Traceability Matrix", link: "/specifications/requirements" },
              { text: "Release Changelog", link: "/specifications/changelog" },
            ],
          },
        ],
        "/ui/": [
          {
            text: "Web Management UI",
            items: [
              { text: "Dashboard Overview", link: "/ui/dashboard" },
              { text: "Diagnostic Test Bench", link: "/ui/testbench" },
              { text: "Runtime Parameter Tuner", link: "/ui/tuner" },
              { text: "Full UI & Diagnostics Guide", link: "/guide/ui-and-diagnostics" },
            ],
          },
        ],
        "/operations/": [
          {
            text: "Operations & Runbooks",
            items: [
              { text: "Monitoring, Health & Runbooks", link: "/operations/monitoring-and-runbooks" },
              { text: "Operational Runbooks", link: "/operations/runbooks" },
              { text: "Push Alerts & Monitoring", link: "/operations/alerts" },
              { text: "Home Assistant Integration", link: "/operations/home-assistant" },
            ],
          },
        ],
        "/specifications/": [
          {
            text: "Specifications & Standards",
            items: [
              { text: "Requirements Traceability Matrix", link: "/specifications/requirements" },
              { text: "Living Dossiers & Hybrid RAG", link: "/specs/dossiers-and-hybrid-rag" },
              { text: "MCP Server Specification (2026-07-28)", link: "/specs/mcp-server-spec" },
              { text: "Release Changelog", link: "/specifications/changelog" },
            ],
          },
        ],
        // Default sidebar when browsing general pages
        "/": [
          {
            text: "Getting Started",
            collapsed: false,
            items: [
              { text: "Quick Start", link: "/guide/getting-started" },
              { text: "Pairing Workflows", link: "/guide/pairing" },
              { text: "Configuration & Environment", link: "/guide/configuration" },
            ],
          },
          {
            text: "Architecture",
            collapsed: false,
            items: [
              { text: "System Topology & Overview", link: "/architecture/overview" },
              { text: "Data Model & Schema", link: "/architecture/data-model" },
              { text: "Security & Storage", link: "/architecture/security" },
            ],
          },
          {
            text: "Management & Pipelines",
            collapsed: false,
            items: [
              { text: "Web Dashboard & Diagnostics", link: "/guide/ui-and-diagnostics" },
              { text: "Pipelines & Ingestion", link: "/guide/pipelines-and-ingestion" },
              { text: "Living Dossiers & Hybrid RAG", link: "/specs/dossiers-and-hybrid-rag" },
              { text: "Vision OCR", link: "/pipelines/vision-ocr" },
            ],
          },
          {
            text: "MCP Specification",
            collapsed: false,
            items: [
              { text: "2026-07-28 MCP Protocol", link: "/specs/mcp-server-spec" },
              { text: "MCP Tools Reference", link: "/mcp/tools" },
              { text: "Fail-Closed Guardrails", link: "/mcp/guardrails" },
            ],
          },
          {
            text: "Operations & Runbooks",
            collapsed: false,
            items: [
              { text: "Monitoring, Health & Runbooks", link: "/operations/monitoring-and-runbooks" },
              { text: "Alerts & Connection Watchdog", link: "/operations/alerts" },
              { text: "Home Assistant Bridge", link: "/operations/home-assistant" },
            ],
          },
          {
            text: "Specifications",
            collapsed: true,
            items: [
              { text: "SRS Requirements Matrix", link: "/specifications/requirements" },
              { text: "Release Changelog", link: "/specifications/changelog" },
            ],
          },
        ],
      },

      socialLinks: [
        { icon: "github", link: "https://github.com/spelech/gmessage-rag" },
      ],

      footer: {
        message: "Released under AGPL-3.0 License.",
        copyright: "Copyright © 2026 Steven T. Pelech and gmessages-rag Contributors",
      },
    },

    markdown: {
      config: (md) => {
        md.use(mathjax3);
      },
    },

    mermaid: {
      theme: "default",
    },
  })
);
