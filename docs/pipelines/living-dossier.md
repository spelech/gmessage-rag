# Living Dossier Engine (`PIPE-03`, `PIPE-04`)

The Living Dossier state engine maintains a continuously updating, human-readable profile for each tracked contact.

## Architectural Principles

1. **State Reconciliation**: Rather than summarizing from scratch, the engine performs a state diff against the previous dossier version.
2. **Temporal Anchoring**: Relative references (*"see you next Tuesday"*, *"tomorrow afternoon"*) are resolved to explicit ISO-8601 calendar dates using the message timestamp.
3. **Chronological Overrides**: New facts supersede old facts when contradictions arise, while historical context is retained.
4. **Markdown Export**: Dossiers are exported to `/data/dossiers/<contact_slug>.md` for direct consumption by Obsidian, local notes tools, or external agents.
5. **Audit History**: Every update creates an audit record in `contact_dossier_history` for change tracking and rollback capabilities.

## Contact Enrichment (`PIPE-04`)

The engine integrates with Google Contacts via `libgm.Client.ListContacts`:
- Populates profile names, phone labels (work, mobile, home), and relationship nicknames.
- Downloads contact avatars and stores them alongside contact dossiers.
