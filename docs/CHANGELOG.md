# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- Integrated Steven T. Pelech's `AgenticEngineeringToolbelt` submodule (`.toolbelt`) with active skills: `engineering-archetype`, `scaffold-project`, `test-harness-builder`, `code-review-foci`, and `mermaid-live`.
- Linked universal and agent-specific instruction rules: `AGENTS.md`, `GEMINI.md`, and `CLAUDE.md`.
- Established traditional Git Flow branch architecture (`main`, `develop`, and `feature/*`).
- Configured 4-stage GitHub Actions CI pipeline (`.github/workflows/ci.yml`) and Dependabot updates (`.github/dependabot.yml`).
- Authored comprehensive living documentation: `docs/ARCHITECTURE.md` (topology, ER diagram, sequence flow) and `docs/REQUIREMENTS.md` (SRS with category taxonomy and test traceability matrix).
- Added `scripts/commit.sh` enforcing Git Flow branch protection and Conventional Commits.
