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
   headers, TLS trust material, etc.)

4. **`TlsOptions` interface** — caller-supplied TLS trust material, reachable
   as `ClientOptions.tls`: `caCert`, `clientCert`, `clientKey`, each typed as
   exactly what `node:tls` accepts for `ca`/`cert`/`key` so the option cannot
   drift from what the transport supports.

   These exist because root DESIGN.md, "RULE: A system-TLS constructor must
   reach a real server", permits delegating to the runtime's default trust
   source *because* a caller can supply their own material when it is not
   enough — and Node's bundled Mozilla root store does not honour a CA an
   operator installed in the host's own store.

   They must be threaded through `new Http2SessionManager(baseUrl, undefined,
   { ca, cert, key })`, **not** `createGrpcTransport`'s `nodeOptions`:
   Connect-ES documents that supplying a `sessionManager` (which this client
   always does, so `close()` has a handle to abort) makes `nodeOptions`
   ineffective, so material passed there would be silently dropped. When `tls`
   is absent the session manager must be constructed with no session options at
   all rather than an object of `undefined`s, so the default trust source is
   provably untouched.

   `createClient` must throw, before any session manager is created, on
   `insecure` combined with any `tls` field (root DESIGN.md, "RULE: Credentials
   over insecure transport require an explicit opt-in" — a plaintext connection
   would ignore the material and ship the bearer token in cleartext behind a
   call site reading as though TLS were configured), and on `clientCert`
   without `clientKey` or the reverse.

Create `src/index.ts` with:

- Re-export of `SpiceDBProtoClient`, `createClient`, `ClientOptions`,
  `TlsOptions`
- Re-export of key proto types from `src/gen/`

### Tests

Use `vitest` for all tests.

Create `src/__tests__/client.test.ts` with:

1. **Factory test** — verify `createClient` returns a client with all service
   properties
2. **Options test** — verify custom options are applied

Create `src/__tests__/custom-tls.test.ts` with a custom-CA fixture test: a
throwaway CA generated in-process, a real gRPC-over-TLS server presenting a
certificate signed by it, and a real client driven against it. Every
connection assertion must be paired — same server, same client, differing only
in whether the material was supplied — since a test that only asserts the
failure cannot tell a rejected certificate from an unreachable port, and one
that only asserts the success cannot tell a verified chain from a disabled one.
Root DESIGN.md, "RULE: A system-TLS constructor must reach a real server",
clause 2.

### Deprecation Handling

Any methods marked deprecated in proto definitions must carry `@deprecated`
JSDoc tags. Note that some services may contain SOME deprecated methods; do
not mark the entire service as deprecated unless ALL exported methods are
marked as such.

### Invariants

- No business logic — only plumbing
- All proto types re-exported as-is
- Generated files under `src/gen/` are never modified
