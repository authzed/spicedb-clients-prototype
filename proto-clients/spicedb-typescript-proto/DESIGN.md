# spicedb-typescript-proto — Design Manifest

- The [root design](../../DESIGN.md) defines shared semantics, RPC coverage, security, and verification.

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

## 2026-10-26 transport contract

The canonical factory is `createSpiceDBClient(endpoint, token, options?)`;
replace the stale `createClient` wrapper name above rather than exporting both.
Add the generated materialize RoaringLookupResourcesService as `materialize`
and export its messages. This is a separate service, not an ExperimentalService
method. Protocol-generated names themselves remain unchanged.

ClientOptions includes `allowInsecureRemoteCredentials?: boolean`; reject remote
plaintext credentials unless it is explicitly true. The loopback guard uses the
same URL parser as the transport and rejects authority-shifting syntax before
opening a session. This transport does not dial Unix sockets: reject Unix
targets rather than treating a DNS hostname `unix` as loopback. TLS material
never supplies another transport mode or bypasses these guards.

All option fields and header collections are readonly inputs. Forward the
caller's AbortSignal and remaining timeout on every RPC shape, including unary
mutations, server streams, and client-streaming import. Already-aborted calls
perform no network work. Cancellation releases HTTP/2 streams and must remain
reachable through exposed iterator cleanup; interceptors preserve auth headers
and never overwrite signal/deadline options. close() terminates owned sessions
and calls. The idiomatic layer alone owns retries, total budgets, chunking,
and native errors; this layer does not restart calls or reset durations.

Export native generated debug and materialize roaring definitions from index.ts.
`SpiceDBProtoClient.materialize` is a Connect client for RoaringLookupResourcesService
created using the same authenticated transport as the other services. Never create
a second session to expose it. Tests exercise a call through the supplied transport.

## 2026-10-28 adoption contract

- **Single protocol copy.** Root DESIGN.md's "Be the single copy of the protocol
  bindings" exists because Go, Python, Ruby and the JVM register descriptors in a
  process-global registry. protobuf-es v2 does not: descriptors are values and
  registries are explicit, and `@authzed/authzed-node` (protobuf-ts) has no global
  registry either. Both bindings load in one process and one bundle without
  conflict, so this tier needs no bridge generator and no aliasing of the legacy
  library. `src/__tests__/legacy-coexistence.test.ts` loads both, constructs both
  clients, and round-trips a caveated relationship through each binding's wire
  format. `@authzed/authzed-node` is a devDependency for that test only.
- **No hand-maintained files in `src/gen/`.** This tier carries no legacy helpers
  bound to generated types; everything under `src/gen/` is buf output.
- **Caller-owned transports.** `new SpiceDBProtoClient(transport)` (no session
  manager) wraps an existing Connect `Transport`; `close()` then releases nothing,
  because the caller owns the transport. The idiomatic client's
  `SpiceDBClient.fromTransport` uses exactly this.
- **Dependency floors.** `@bufbuild/protobuf` ^2.11.0 (the runtime must be at
  least the pinned protoc-gen-es v2.11.0), `@connectrpc/connect` and
  `@connectrpc/connect-node` ^2.0.0, verified by building and running this tier's
  and the idiomatic client's suites against exactly those versions.
  `@types/node` is an optional peer (>=20.19.0) so consumers are not forced onto
  newer Node typings.
