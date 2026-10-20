# Changelog

## Unreleased

Initial SQL-focused dbunit implementation:

- Explicit inline, file, filesystem, AFS, and embedded fixtures.
- Toolbox data expressions and custom UDFs with per-run state.
- SQLX batch/native preparation with presence-preserving conversion.
- Owned, borrowed, rollback, and nontransactional execution policies.
- Keyed, ordered, and multiset assertions with structured reports.
- Native schema extraction/application, query snapshots, and comparisons.
- Fixture hydration, deterministic local sequences, UUIDs, references and captures.
- Logical-to-physical table mappings with dependencies and conflict detection.
- Go testing helpers and Endly workflow/MCP integration.

BigQuery live certification and advanced legacy dsunit compatibility remain outstanding; see `plan.md`.
