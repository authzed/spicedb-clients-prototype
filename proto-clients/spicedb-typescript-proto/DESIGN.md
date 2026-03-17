# spicedb-typescript-proto — Design Manifest

This is the TypeScript proto client for SpiceDB. It wraps buf-generated
Connect-ES stubs with minimal boilerplate for connection setup and
authentication.

## Generated Files

Everything under `src/gen/` is produced by `buf generate` and MUST NOT be
manually modified.

## Additional Code (for Claude)

### Required Exports

Create `src/client.ts` with:

1. **`SpiceDBProtoClient` class** — wraps all generated Connect-ES service
   clients:
   - `permissions` — PermissionsService client
   - `schema` — SchemaService client
   - `watch` — WatchService client
   - `experimental` — ExperimentalService client

2. **`createClient(endpoint: string, token: string, options?: ClientOptions)`**
   — factory function that creates a transport with bearer token headers and
   returns a `SpiceDBProtoClient`.

3. **`ClientOptions` interface** — for optional configuration (insecure, custom
   headers, etc.)

Create `src/index.ts` with:

- Re-export of `SpiceDBProtoClient`, `createClient`, `ClientOptions`
- Re-export of key proto types from `src/gen/`

### Tests

Use `vitest` for all tests.

Create `src/__tests__/client.test.ts` with:

1. **Factory test** — verify `createClient` returns a client with all service
   properties
2. **Options test** — verify custom options are applied

### Deprecation Handling

Any methods marked deprecated in proto definitions must carry `@deprecated`
JSDoc tags. Note that some services may contain SOME deprecated methods; do
not mark the entire service as deprecated unless ALL exported methods are
marked as such.

### Invariants

- No business logic — only plumbing
- All proto types re-exported as-is
- Generated files under `src/gen/` are never modified
