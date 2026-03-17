# SpiceDB Clients Monorepo

This is a monorepo of SpiceDB client libraries for Go, Python, and TypeScript.

## Structure

- `DESIGN.md` — Read this first. It defines the idiomatic vision and backwards
  compatibility rules for all clients.
- `proto-clients/` — buf-generated proto clients. These are internal
  dependencies, not for direct end-user consumption. Don't modify buf-generated
  files.
- `spicedb-go/`, `spicedb-python/`, `spicedb-typescript/` — Idiomatic clients.
  These are the public-facing libraries.
- Each idiomatic client has an `examples/` directory that doubles as integration
  tests.

## Rules

- NEVER break backwards compatibility in public APIs (unless this is the first implementation)
- Deprecated proto methods must be marked deprecated in both client tiers
- Read the per-directory DESIGN.md and CLAUDE.md before making changes in any
  subdirectory
