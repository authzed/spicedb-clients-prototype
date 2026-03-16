# spicedb-typescript-proto

This is a buf-generated TypeScript proto client for SpiceDB using Connect-ES.

## What to do

1. Read DESIGN.md — it specifies exactly what code to add beyond the buf output
2. Only add code specified in the DESIGN.md manifest — nothing extra
3. Don't touch files under `src/gen/` — those are produced by buf generate
4. Mark deprecated proto methods with `@deprecated` JSDoc tags
5. Run `yarn test` after making changes
