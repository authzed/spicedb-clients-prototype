# spicedb-typescript-proto

This is a buf-generated TypeScript proto client for SpiceDB using Connect-ES.

## What to do

1. Read DESIGN.md — it specifies exactly what code to add beyond the buf output
2. Only add code specified in the DESIGN.md manifest — nothing extra
3. Don't touch files under `src/gen/` — those are produced by buf generate
4. Mark deprecated proto methods with `@deprecated` JSDoc tags
5. Run `pnpm test` after making changes

## Test prerequisites

`openssl` must be on `PATH`. The custom-TLS fixture —
`src/__tests__/custom-tls.test.ts` — shells out to it to generate a throwaway CA
and sign the certificate its real TLS server presents. It **fails** rather than
skips when openssl is missing (a skipped test reads as coverage while proving
nothing), so on a minimal image you get `spawnSync openssl ENOENT` with no hint.
Every other test here runs without it. CI is fine: the workflows run on
`depot-ubuntu-24.04-arm-*`, which ships openssl.
