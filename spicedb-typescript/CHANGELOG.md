# Changelog

## Unreleased

### Added

- **2026-08-19**: An escape hatch, `SpiceDBClient.raw()`. It returns the
  underlying `SpiceDBProtoClient` — the four generated Connect clients
  (`permissions`, `schema`, `watch`, `experimental`) this library makes its own
  calls through — so a request the idiomatic API cannot express has a
  workaround short of forking the client:

  ```typescript
  const { permissionship } = await client.raw().permissions.checkPermission({
    consistency: { requirement: { case: "fullyConsistent", value: true } },
    resource: { objectType: "document", objectId: "readme" },
    permission: "view",
    subject: { object: { objectType: "user", objectId: "jimmy" } },
  });
  ```

  Clearly-marked **secondary** API — root DESIGN.md's "What NOT To Do" keeps
  channels, stubs and metadata out of the primary surface and permits exactly
  this ("escape hatches for advanced use are acceptable as clearly marked
  secondary API"). No stability promise beyond what `@connectrpc/connect` and
  the generated `@spicedb/proto` clients give. The type is re-exported as
  `SpiceDBProtoClient` so callers can name it without depending on
  `@spicedb/proto` directly.

  The `authorization` header comes free (it is set by a transport interceptor),
  but a raw call gets no `SpiceDBError` mapping, no retry, and no
  `defaultTimeoutMs` — pass `CallOptions.timeoutMs` yourself. Do not call
  `close()` on the returned object: it is this client's own connection, and
  `SpiceDBClient.close()` is what releases it.

  It is an accessor, not a constructor: it takes no endpoint, token, or
  transport setting, so transport construction stays on the single guarded path
  in the proto tier and this cannot become a route around root DESIGN.md,
  "RULE: Credentials over insecure transport require an explicit opt-in".

  New example: `examples/raw_escape_hatch/`.

- **2026-08-19**: Caller-supplied TLS trust material, via a new `tls` option on
  both `new SpiceDBClient({ ... })` and `createSpiceDBClient(...)`, plus the
  new exported `TlsOptions` type. Purely additive — an existing call site is
  byte-identical in behavior, and omitting `tls` leaves the transport's trust
  source untouched.
  - `tls.caCert` — root certificate(s) used to verify SpiceDB's certificate.
    Supply this to reach a SpiceDB fronted by a private or corporate CA. It
    replaces Node's bundled roots for that client rather than adding to them.
  - `tls.clientCert` / `tls.clientKey` — the client's own certificate chain and
    private key, for a server requiring mutual TLS. Both must be supplied
    together; either alone throws.

  Each field is typed as exactly what `node:tls` accepts for `ca`/`cert`/`key`
  (a PEM string, a `Buffer`, or an array of either), so the option cannot drift
  from what the transport supports. Without these, a private-CA deployment was
  unreachable: Node ships a bundled Mozilla root store, so a CA installed in
  the host's own store is not honoured. Root DESIGN.md, "RULE: A system-TLS
  constructor must reach a real server", permits delegating to that bundled set
  precisely because a caller can supply their own material instead — which is
  now true.

  Trust material never changes whether TLS is used. Combining any `tls` field
  with `insecure: true` throws at construction rather than being silently
  ignored (which is what `node:tls` would do on a plaintext socket, while the
  call site read as though TLS were configured), so this cannot become a
  quieter route around root DESIGN.md, "RULE: Credentials over insecure
  transport require an explicit opt-in". The existing loopback guard still
  applies first and unchanged.

  ```typescript
  const client = createSpiceDBClient("spicedb.internal:443", token, {
    tls: { caCert: readFileSync("/etc/ssl/certs/internal-ca.pem") },
  });
  ```

- **2026-08-18**: Error mapping now carries the server's detail all the way to
  the caller, per root DESIGN.md, "RULE: Error mapping must not lose the
  server's detail". Purely additive — `SpiceDBErrorOptions` extends
  `ErrorOptions`, so every existing `new NotFoundError(msg, { cause })` call
  site is unchanged.
  - Two new error classes, both `SpiceDBError` subclasses and both exported
    from the package root:
    - `OutOfRangeError` for `Code.OutOfRange`, SpiceDB's code for an expired or
      garbage-collected ZedToken. It previously fell through to the base
      `SpiceDBError`, so the one recoverable error in a token-threading
      application was indistinguishable from an internal fault. Recovery is
      mechanical: discard the stale token and re-read at full consistency.
    - `UnauthenticatedError` for `Code.Unauthenticated` — a wrong, expired, or
      rotated API token, previously also indistinguishable from an internal
      fault. Distinct from `PermissionDeniedError`, which means the caller was
      identified but not allowed.
  - Every `SpiceDBError` now carries the `google.rpc.ErrorInfo` detail SpiceDB
    attaches to a status, on three new readonly properties: `reason` (the name
    of an `authzed.api.v1.ErrorReason` enum value, e.g.
    `"ERROR_REASON_MAXIMUM_DEPTH_EXCEEDED"`), `reasonDomain` (`"authzed.com"`
    for SpiceDB), and `reasonMetadata` (the specifics behind the reason, such
    as which precondition failed). The reason is surfaced exactly as the server
    sent it: a value a newer server knows and this client does not is passed
    through unchanged rather than coerced or rejected, per root DESIGN.md's
    "RULE: A conversion that cannot preserve meaning must fail", which requires
    server-supplied unknowns to degrade rather than throw. `reason` is `""` and
    `reasonMetadata` `{}` when the server attached no `ErrorInfo`. Per-item
    bulk errors (`toSpiceDBErrorFromStatus`) carry them too — that function now
    also accepts the status's `details`.
  - New exported type `SpiceDBErrorOptions`, the options bag every error class
    accepts.

  ```ts
  try {
    await client.write(txn);
  } catch (err) {
    if (err instanceof FailedPreconditionError) {
      console.log(err.reason, err.reasonMetadata.precondition_resource_id);
    } else if (err instanceof OutOfRangeError) {
      // ZedToken expired or GC'd: drop it and re-read at full consistency.
    }
  }
  ```

  `@spicedb/proto` now also exports `ErrorInfo`/`ErrorInfoSchema` (from
  `google/rpc/error_details.proto`, newly added as a `buf generate` input) and
  `ErrorReason`/`ErrorReasonSchema`, so callers who want the generated enum can
  compare against it directly. Both remote plugins in the proto tier's
  `buf.gen.yaml` are now pinned (`bufbuild/es:v2.11.0`, matching the pinned
  `@bufbuild/protobuf` runtime, and `connectrpc/es:v1.6.1`) — they were
  unversioned, so any `mage gen` would have silently rewritten all of `src/gen`
  with a newer codegen.

- **2026-08-17**: `checkPermission`/`checkPermissions`/`checkAny`/`checkAll`
  gain a call-level default caveat context via a new `CheckOptions` type
  (`{ context?: Record<string, unknown> }`). Previously the only way to
  supply caveat context was per-item, on each `CheckRequest.context` — there
  was no way to set one default across a whole check/bulk-check call, so a
  caller checking many items with the same caveat context had to repeat it
  on every `CheckRequest`. `checkPermission` accepts `CheckOptions` as a new
  optional third argument. `checkPermissions`/`checkAny`/`checkAll` gain a
  second, explicit-array overload — `(consistency, checks: CheckRequest[],
  options?: CheckOptions)` — since their existing variadic form
  (`consistency, ...checks`) has nowhere to put a trailing options argument;
  that variadic form is completely unchanged and never produces a
  call-level default. The proto wire has no request-level context field
  (`CheckBulkPermissionsRequest` carries no `context`, only
  `CheckBulkPermissionsRequestItem.context`), so `options.context` is fanned
  out onto every item at request-build time and merged key-by-key with that
  item's own `context`: the item's own keys win on conflict, and call-level
  keys the item doesn't mention are retained (not a wholesale replacement).
  If neither is supplied, no context field is set on the request (never an
  empty Struct). Purely additive — no existing call site changes.
  `CheckOptions` is exported from the package root.

  ```typescript
  // Per-item context (existing, unchanged):
  await client.checkPermission(consistency, { ...check, context: { now: 42 } });

  // New: a call-level default, applied to every item in a bulk check:
  await client.checkPermissions(
    consistency,
    [check1, check2],
    { context: { now: 42 } },
  );
  ```

- **2026-08-17**: `LookupResource` and `LookupSubject` gain a `lookedUpAt`
  field: the revision that result was computed at (from the response's
  `looked_up_at` ZedToken). It is identical for every item yielded by a
  single `lookupResources`/`lookupSubjects` call — a property of the call,
  not of the individual resource/subject. Thread it into
  `atLeast()`/`atLeastOrFull()` for read-your-writes on a later call.
  Additive — existing destructuring of `LookupResource`/`LookupSubject`
  continues to work unchanged. Mirrors spicedb-go's
  `LookupResource.LookedUpAt`/`LookupSubject.LookedUpAt`
  (`client/lookup_types.go`).

- **2026-08-16**: Added `DeadlineExceededError` and `ResourceExhaustedError`
  to the typed error hierarchy, and fixed `RESOURCE_EXHAUSTED` (e.g. a rate
  limit) to map to the new `ResourceExhaustedError` instead of being folded
  into `UnavailableError`. This brings TypeScript's error hierarchy in line
  with the canonical nine-type set already present in Go, Java, Python,
  Rust, Ruby, and C#. Only which typed class the code maps to changed here;
  both new error classes are exported from the package root.
  **`RESOURCE_EXHAUSTED` was subsequently removed from the retryable set** --
  see the 2026-08-18 retry-safety entry below, which is the shipped behavior.
  (This entry originally read "was already, and remains, treated as
  transient".)

- **2026-08-15**: `readRelationships()`, `lookupResources()`,
  `lookupSubjects()`, `exportBulkRelationships()`, and `watch()` now retry
  stream ESTABLISHMENT on transient errors -- as shipped, `UNAVAILABLE` and
  `ABORTED`; `RESOURCE_EXHAUSTED` was in that set when this landed and was
  removed by the 2026-08-18 retry-safety entry below -- reusing the same
  `isTransientError`
  predicate and exponential backoff as `withRetry`. Retry is scoped strictly
  to (re-)opening the stream: once any item has been yielded to the caller
  from the current stream, a later transient error is never retried — it is
  surfaced as-is, since retrying after a yield would replay/duplicate
  already-delivered items. `watch()` in particular never retries mid-watch,
  only before the first update of a given `watch()` call is yielded. Mirrors
  spicedb-python's `_should_retry_establishment` approach
  (`spicedb-python/spicedb/client.py`). No public API change.

- **2026-08-15**: `deleteRelationships()` now accepts an optional
  `DeleteOptions` second argument with `mustMatch`/`mustNotMatch`
  (each `RelationshipFilterOptions[]`) and `limit`, mirroring spicedb-go's
  `WithDeleteMustMatch`/`WithDeleteMustNotMatch`/`WithDeleteLimit`
  (`spicedb-go/client/relationships.go`) and spicedb-python's
  `delete_relationships` keyword arguments. Previously the proto's
  `optionalPreconditions`/`optionalLimit` fields were unreachable, so there
  was no way to do a precondition-guarded or bounded delete. Preconditions
  are built the same way as `Transaction.mustMatch`/`mustNotMatch`. Setting
  `limit` also sets `optionalAllowPartialDeletions: true` — the server
  otherwise rejects a limited delete that finds more matches than the limit.
  Additive — existing `deleteRelationships(filter)` call sites are
  unaffected (no preconditions, no limit, `optionalAllowPartialDeletions:
  false`, same as before). `DeleteOptions` is exported from the package
  root.

  ```typescript
  // Only delete if an `owner` relationship still exists on the resource:
  const revision = await client.deleteRelationships(filter, {
    mustMatch: [{ resourceType: "document", resourceId: "1", resourceRelation: "owner" }],
    limit: 1000,
  });
  ```

### Fixed

- **2026-08-19**: **The example set is pinned by name, not by count.** `wantExampleCount` passed
  unchanged when an example directory was *renamed* -- only deletion was caught, and a manifest
  can drift from disk with no signal. `wantExamples` now lists every example by name and is
  reconciled with the glob in both directions, the same shape the skip targets already used.
  Verified by renaming `examples/lookup_subjects`: `expected but absent: [lookup_subjects];
  present but not expected: [lookup_subj]`.

- **2026-08-19**: **The example runner now asserts how many examples it ran.** `IntegrationTest`
  globbed `examples/*/index.ts` and skipped `watch_changes` by a hardcoded name comparison. A glob
  cannot list an example that is not there, so a renamed or moved example produced a *shorter*
  run that still reported green -- the same failure mode `.github/workflows/rust.yaml` already
  guards against by grepping its test output for `"1 passed"`. The runner now checks the glob
  against an expected count, checks that every skipped name is an example that exists, and fails
  if the number executed is not `count - skipped`. New `mage checkExamples` runs those checks
  without a server and is called from `mage test`, so the unit job catches a broken wiring too.
  `watch_changes` is still skipped, but by a named entry that prints its reason and is counted.
  Root DESIGN.md, "RULE: An example must be executed by CI and must be able to fail", clause 1.

- **2026-08-19**: **Examples now read `SPICEDB_ENDPOINT` and `SPICEDB_TOKEN`**, defaulting to
  `localhost:50051` and `testtoken`. `examples/README.md` had told the reader to export both
  variables since the directory existed, and no example read either one, so the documented setup
  silently did nothing. `docker-compose.test.yml` takes its published port from
  `SPICEDB_TEST_PORT` and its key from `SPICEDB_TEST_TOKEN` (same defaults), and
  `mage integrationTest` derives the port from `SPICEDB_ENDPOINT`, so the suite can run on a host
  whose 50051 is occupied. `custom_tls/` is unchanged: it stands up its own TLS-terminated server
  and never talks to the shared one.

- **2026-08-19** (documentation only): `examples/README.md` claimed `mage test` "starts a SpiceDB
  container automatically". It does not, and never did -- `mage integrationTest` is the target
  that starts one. The README now says which target does what, and which examples CI executes.

- **2026-08-19**: **A large bulk check is no longer sent as one oversized request.**
  `checkPermissions`, `checkPermission`, `checkAny` and `checkAll` built a single
  `CheckBulkPermissions` request from however many checks the caller passed. SpiceDB caps a
  request at `maxBulkCheckCount` -- 10,000, a hard-coded const in
  `internal/services/v1/bulkcheck.go` with no flag to raise or lower it -- and rejects anything
  larger with `ERROR_REASON_TOO_MANY_CHECKS_IN_REQUEST`. Nothing in the proto enforced the cap
  either (`CheckBulkPermissionsRequest.items` carries only a per-item `required` rule, not a
  collection-size rule), so the failure surfaced only at runtime, on the largest inputs.

  Checks are now split into requests of at most 1,000 items -- the same batch size the import
  path already uses, and the value `spicedb-rust` (the one client that already chunked) picked
  -- and the responses are concatenated in input order, so `results[i]` still corresponds to the
  caller's i-th relationship across a chunk boundary. The response-length guard added earlier on
  this branch now runs per chunk. A caller passing fewer than 1,000 relationships still makes
  exactly one request.

  Three consequences of the split are worth stating outright, because they change contracts
  callers may already depend on:

  - **The malformed-pair message now names the caller's own index.** Its `check item N` prefix is
    computed from an absolute offset, not from the position within whichever request carried the
    failure. Without that, a failure at relationship 1,003 reported as `check item 3` — the same
    misattribution the response-length guard exists to prevent, relocated into the diagnostic.
    This client's *per-item error* path is unaffected: it routes the item's own status straight
    through the error mapper and never carried a `check item N` prefix to get wrong. Only
    `spicedb-go` and `spicedb-java` index that path.
  - **`checked_at` is per response, and a response is now one chunk.** Results from a single
    request still share one token; an input large enough to be split carries more than one across
    the returned collection. Root DESIGN.md's bulk-check invariant has been re-scoped to match.
  - **A per-call timeout, and the retry budget, bound each request rather than the whole call.**
    Worst-case wall time for `n` checks is `ceil(n / 1000) x timeout`. This is deliberate: one
    deadline spanning every chunk would make a large check fail purely for being large, and a
    retry budget shared across chunks would let one flaky chunk exhaust the allowance for the
    rest. The docs now say so.

  Retry is applied per chunk, so a transient failure on the third chunk never re-sends the first
  two. A caller passing *zero* checks now makes no request at all and resolves to `[]`;
  previously one empty `CheckBulkPermissions` request went out.

- **2026-08-18**: **Security — a bypass in the guard that refuses to send credentials over
  plaintext to a non-loopback host was fixed.** Creating an insecure client for
  `"127.0.0.1:443@evil.com"` was accepted as loopback and sent the bearer token to `evil.com`
  in cleartext, with no opt-in and nothing reported. `isLoopbackEndpoint` split the endpoint on
  its last colon and read the host as `127.0.0.1`; `Http2SessionManager` computes
  `new URL("http://127.0.0.1:443@evil.com").origin`, which reads `127.0.0.1:443` as *userinfo*
  and yields `"http://evil.com"` — the authority it then hands straight to `http2.connect`.

  The root cause was that the guard parsed the endpoint differently than the transport did, so
  the fix is not a tighter split: `isLoopbackEndpoint` now builds the exact URL the client
  dials and asks `URL` — the same parser `Http2SessionManager` uses — for its `hostname`.
  Guard and transport can no longer disagree. Endpoints containing `@`, `/`, `?`, `#`, or
  whitespace are additionally refused outright, since a legitimate SpiceDB target contains none
  of them — `\` too, which WHATWG `URL` treats as `/` for special schemes. A bare IPv6 literal
  (`"::1"`) is bracketed — for the guard's parse **and** for the `baseUrl` the transport dials,
  both via one `transportAuthority` helper, so the two cannot disagree — and keeps working, as
  do `localhost` and 127.0.0.0/8.

- **2026-08-18**: **Bare `::1` now actually connects.** It satisfied the guard but then threw
  `TypeError: Invalid URL` from `new URL()`, because the guard bracketed the literal for its own
  parse while `createSpiceDBClient` built its `baseUrl` from the raw endpoint (`"http://::1"` is
  not a legal URL). `"::1"`, `"[::1]"`, `"0:0:0:0:0:0:0:1"` and `"[::1]:50051"` all construct a
  client.

- **2026-08-18**: Tests in this package now resolve `@spicedb/proto` to the proto package's
  **source** rather than its built `dist/` (new `vitest.config.ts` alias). The workspace link
  resolves to `dist/index.js`, and `Magefile.go`'s `Test()` builds only the package under test,
  so a local `npm test` here exercised whatever `dist/` was last built by hand — which hid a
  guard fix and let these tests pass against a version of `isLoopbackEndpoint` that still had
  the bypass. CI was never affected (`.github/workflows/typescript.yaml` builds
  `@spicedb/proto` in all four jobs); this closes the local gap.

- **2026-08-18**: **Breaking, and security-motivated: `unix:` endpoints are now refused instead
  of being treated as loopback.** `createSpiceDBClient("unix:/var/run/spicedb.sock", token,
  { insecure: true })` previously passed the guard on the grounds that "a unix socket never
  leaves the host's kernel" — but Connect-ES over Node's `http2` has no unix-domain-socket
  support reachable from a `baseUrl`. `new URL("http://unix:/var/run/spicedb.sock").origin` is
  `"http://unix"`, and `Http2SessionManager` hands exactly that to `http2.connect`, so it
  resolved the **DNS name `unix`** and shipped the bearer token there in cleartext on port 80
  while the guard reported "loopback". Nothing was ever connecting to a socket path, so no
  working configuration breaks.

  Such an endpoint now throws, unconditionally — before the credential guard, and regardless of
  TLS or `allowInsecureRemoteCredentials`, since neither makes "resolve a hostname called
  `unix`" what the caller asked for. The Go, Python and Ruby clients keep their `unix:`
  exemption; their transports genuinely dial the path.

- **2026-08-18**: `importBulkRelationships` required a materialized array, and then held the
  dataset twice. The signature was `relationships: Relationship[]`, and the body ran
  `relationships.map(toProtoRelationship)` before streaming, so the caller's array and a full
  array of protos were both resident before a single byte went out. That is the wrong shape for
  the one method whose entire purpose is volume: a caller with a dataset larger than memory had
  no way to import it, however lazily they could produce it. It now accepts
  `Iterable<Relationship> | AsyncIterable<Relationship>` -- an array, a generator, an async
  generator, anything with `Symbol.iterator` or `Symbol.asyncIterator` -- and converts and batches
  relationships (1,000 per request message, unchanged) as they are pulled, so only one batch is
  ever resident.

  Widening only; every existing call site is unaffected, since arrays are iterable, and an array
  is still the right choice when the data is already in memory. This brings the last client into
  line: Go takes `iter.Seq`, C# `IAsyncEnumerable`, Java `Iterable`, Python `Iterable`, Ruby
  Enumerable, Rust `IntoIterator`.

  ```ts
  // Before and after -- unchanged:
  await client.importBulkRelationships([rel1, rel2, rel3]);

  // New: stream from a cursor without materializing anything.
  async function* fromCursor() {
    for await (const row of db.query("SELECT ...")) {
      yield relationship(`document:${row.id}`, "viewer", `user:${row.userId}`);
    }
  }
  await client.importBulkRelationships(fromCursor());
  ```

  The sequence is consumed exactly once, which is safe because a bulk import is a mutation and is
  never retried automatically (root DESIGN.md, "RULE: Automatic retry is for idempotent operations
  only"). A caller retrying by hand must pass a fresh iterable -- a spent generator yields nothing
  and would import zero relationships. New tests in `src/__tests__/import-streaming.test.ts` assert
  on *when* the caller's sequence is pulled (the first batch reaches the server before the
  generator is exhausted), not just on the returned count, which a buffering implementation would
  satisfy too.

- **2026-08-18**: Every streaming call (`readRelationships`, `lookupResources`, `lookupSubjects`,
  `watch`, `exportBulkRelationships`) leaked one HTTP/2 stream and one server-side SpiceDB
  dispatch, permanently, whenever a caller stopped consuming before the stream was exhausted --
  the single most common streaming idiom ("take the first N results, stop"). Connect-ES's
  server-streaming iterator deliberately omits `return()`/`throw()` (see its `run-call.js`, "We
  deliberately omit throw/return"), so a `for await` `break` never reached the transport, and the
  client never passed `CallOptions.signal` at all. Each streaming method now accepts an optional
  `signal?: AbortSignal` (a new `options` parameter for `readRelationships`/
  `exportBulkRelationships`, a new field on `LookupResourcesParams`/`LookupSubjectsParams`/
  `WatchOptions`) and internally links its own `AbortController` through `CallOptions.signal` on
  every attempt, aborting it in a `finally` block that runs on normal completion, a thrown error,
  AND a caller `break` (which resumes the generator via `.return()`, unwinding through the same
  `finally`) -- so the underlying HTTP/2 stream is released on abandonment regardless of whether
  the caller passes its own signal. See root DESIGN.md, "RULE: Abandoning a stream must release
  it".
- **2026-08-18**: Added `SpiceDBClient.close(): void` (and `SpiceDBProtoClient.close(): void` in
  `@spicedb/proto`) to release the underlying HTTP/2 connection deterministically. Idempotent --
  safe to call more than once. Previously there was no way to release the transport at all short
  of process exit; every streaming call shares one connection via `Http2SessionManager`, which
  `createGrpcTransport` now receives pre-built (rather than building one internally) specifically
  so `close()` has a handle to abort.
- **2026-08-18**: Watch resumability. `WatchOptions` gains
  `includeCheckpoints?: boolean`, which requests
  `WATCH_KIND_INCLUDE_CHECKPOINTS` (alongside
  `WATCH_KIND_INCLUDE_RELATIONSHIP_UPDATES`, since a non-empty
  `optionalUpdateKinds` replaces the server's implicit default rather than
  adding to it).
  - `WatchEvent.isCheckpoint` and `WatchEvent.schemaUpdated` both already
    existed and were both already assigned from the response on every event
    (`client.ts`'s `watch()`) — but since nothing could ever request
    `WATCH_KIND_INCLUDE_CHECKPOINTS` or `WATCH_KIND_INCLUDE_SCHEMA_UPDATES`,
    the server never had a reason to send either, so both were always
    `false` in practice and the `examples/watch_changes/`
    `if (event.isCheckpoint)`/`if (event.schemaUpdated)` branches were both
    unreachable. This task is in scope for checkpoints only: wired up
    `includeCheckpoints` to make `isCheckpoint` reachable, rather than
    adding a parallel field. `schemaUpdated` remains permanently `false` —
    still unreachable, since nothing yet requests
    `WATCH_KIND_INCLUDE_SCHEMA_UPDATES`; that field's `if` branch in the
    example is left as documented but presently dead code, same as before
    this change, pending a future schema-update-support task.
  - `WatchEvent.revision` was already populated from
    `WatchResponse.changesThrough` — the proto's resume token ("This token
    can be used in a subsequent WatchRequest to resume watching from this
    point") — but undocumented as such. Documented its resumability role on
    both `WatchEvent.revision` and `WatchOptions.startRevision`.
  - Without checkpoints, a watch on a quiet namespace behind an idle-timeout
    proxy (ALB, nginx, Envoy) is killed with no changes to resume from
    beyond the original `startRevision` — reprocessing everything since,
    possibly past the GC window.
  - `examples/watch_changes/index.ts` updated to request checkpoints and
    exercise both branches; `src/__tests__/watch-operation-mapping.test.ts`
    gains coverage asserting `includeCheckpoints` reaches the built
    `WatchRequest` and that a checkpoint event is distinguishable from one
    carrying updates.
- **2026-08-18**: Call deadlines, per root `DESIGN.md` "RULE: A unary call must have a
  deadline". Previously no method accepted a timeout and no client-level default existed, so a
  SpiceDB instance that accepted a connection but never answered hung every caller forever — the
  connection looks fine at the transport level, so no error is produced and there is nothing for
  retry logic to act on.
  - Every unary method gained an optional `timeoutMs` (milliseconds) — either a trailing
    `options?: { timeoutMs?: number }` parameter (`write`, `readSchema`, `writeSchema`,
    `diffSchema`, the three experimental counter methods), or a new
    field on an existing options type (`CheckOptions`, `DeleteOptions`,
    `ExpandPermissionTreeParams`, `ReflectSchemaOptions`, `ComputablePermissionsParams`,
    `DependentRelationsParams`) — passed straight through as Connect's `CallOptions.timeoutMs`.
    Additive; existing call sites are unaffected. `checkPermissions`/`checkAny`/`checkAll`'s
    classic variadic form still carries no options (unchanged, per its existing doc comment);
    the explicit-array form picks up `timeoutMs` via `CheckOptions` automatically.
  - `SpiceDBClientOptions` gained `defaultTimeoutMs`, applied to any unary call that doesn't
    supply its own `timeoutMs`. Defaults to 30 seconds (`DEFAULT_TIMEOUT_MS`), mirroring
    `authzed-node`'s `DEFAULT_DEADLINE_MS = 30_000` (its comment cites `grpc/grpc-node#541`).
    There is deliberately no way to construct a client whose unary calls have no bound at all.
  - Streaming methods (`readRelationships`, `lookupResources`, `lookupSubjects`, `watch`,
    `exportBulkRelationships`) do **not** accept `timeoutMs` and are **not** bound by
    `defaultTimeoutMs` — DESIGN.md's "Streaming calls MUST NOT inherit the unary default": these
    are long-lived by design (`watch` may legitimately run for the life of the process), and a
    30s cutoff would end a legitimate stream, which is a worse defect than the one this change
    fixes.
  - `DeadlineExceededError` (added earlier, but never actually produced by this client since
    nothing enforced a deadline) is now reachable: a timed-out call rejects with it, not a
    generic `SpiceDBError`. `Code.DeadlineExceeded` is not in `TRANSIENT_CODES`, so a timeout is
    never auto-retried.
  - **Fix round 1 correction:** `importBulkRelationships` also gained an `options?: {
    timeoutMs?: number }` parameter, but — unlike the unary methods above — it is
    client-streaming, not unary, and is now explicitly **excluded** from `defaultTimeoutMs`: its
    duration scales with the size of the caller's dataset, not with server latency, so no fixed
    default is correct for it (root DESIGN.md, "RULE: A unary call must have a deadline",
    clause 3, amended to cover client-streaming and bidirectional RPCs, not only
    server-streaming). Omitting `options.timeoutMs` now means unbounded (Connect's
    `createDeadlineSignal` sets no timer when `timeoutMs` is `undefined`); passing it still
    bounds the call. An earlier version of this fix incorrectly resolved `timeoutMs` against
    `defaultTimeoutMs` for this call, which would have silently aborted large, legitimate
    multi-minute imports at 30 seconds.
  - **Fix round 1 correction (H1):** `createSpiceDBClient`'s inline options type — the documented
    construction path this file's own example above uses — had NOT been widened to include
    `defaultTimeoutMs`, so the documented `createSpiceDBClient(endpoint, token, {
    defaultTimeoutMs: 5000 })` literally failed to type-check (`TS2353`). Nothing in the test
    suite constructed through that factory, so the gap went uncaught. Widened the factory's
    options type, regenerated `etc/client.api.md` (purely additive), and `spicedb-gen`'s
    generated `TypedClient.create()` — which forwarded the same narrow `{ insecure?: boolean }`
    type — now forwards the full options object (`headers`, `maxRetries`, `defaultTimeoutMs`)
    too.
  - `spicedb-gen`'s TypeScript typed-client template needed one change (see the H1 correction
    above for `TypedClient.create()`); its generated `check()` already forwarded a
    caller-supplied `options?: CheckOptions` straight through to `checkPermission`, so it picked
    up `timeoutMs` automatically with no template change. Verified by regenerating
    `testdata/typescript/permissions.ts` against the updated package and type-checking clean
    (including `type_errors.ts`'s `@ts-expect-error` assertions, unaffected).
  - New `src/__tests__/deadline.test.ts`, using `createRouterTransport` (from
    `@connectrpc/connect`) instead of the `vi.fn()`-mocked `proto` field used elsewhere in this
    suite — deadline enforcement lives in Connect's own transport machinery
    (`protocol/signals.js`'s `createDeadlineSignal`), which a mock bypasses entirely.
    `createRouterTransport` runs the real client-side transport stack against a real (in-process)
    handler that deliberately stalls: a unary call against a stub that never responds rejects
    with `DeadlineExceededError` well before the stall completes, a per-call `timeoutMs`
    overrides a much larger client default, a streaming call outlives a tiny unary default
    instead of inheriting it, bulk import is both unbounded by the default and still honors an
    explicit `timeoutMs`, and constructing through `createSpiceDBClient` (not just `new
    SpiceDBClient`) with `defaultTimeoutMs` actually applies it end-to-end. Every test is wrapped
    in a watchdog so a regression fails the suite instead of hanging CI.
  - New `examples/call_deadlines/`, run against a real SpiceDB rather than a mock: constructs a
    client via the documented `defaultTimeoutMs` option on `createSpiceDBClient`, overrides it
    per-call, and confirms bulk import is unbounded by default.
- **2026-08-18**: Retry safety, per root `DESIGN.md` "RULE: Automatic retry is for idempotent
  operations only". Three changes:
  - `RESOURCE_EXHAUSTED` is no longer retried. In SpiceDB it signals memory load-shed (retrying
    adds load to an already-overloaded server) or a deterministic `MaxDepthExceeded` (retrying can
    never succeed — it re-runs the most expensive class of check several times before surfacing
    the same error). Previously `Code.ResourceExhausted` was in `errors.ts`'s `TRANSIENT_CODES`.
  - Mutations (`write`, `deleteRelationships`, `writeSchema`, `importBulkRelationships`, and the
    experimental counter register/unregister calls) are no longer retried on a transient error,
    even though the underlying code is retryable. A `write()` carrying `OPERATION_CREATE` or
    preconditions is not idempotent: if it commits and the response is lost (a rolling restart, a
    proxy dropping the connection), a retry would surface `ALREADY_EXISTS`/`FAILED_PRECONDITION`
    for a write that in fact succeeded, and the caller would wrongly conclude it had failed. Reads
    still retry automatically. All six mutation call sites previously routed through `withRetry`;
    they now go through a new private `callOnce`, which converts the error without retrying.
  - Backoff is now full-jitter (`Math.random() * cap`) instead of plain exponential doubling.
    Without jitter, every client in a fleet retries on the same schedule after a server restart,
    turning the recovery into a thundering herd.

  `src/__tests__/errors.test.ts`'s `isTransientError` suite had an `it("returns true for
  ResourceExhausted", ...)` case; it is renamed `"returns false for ResourceExhausted"` and
  inverted, since the old assertion was exactly the defect this fixes. New coverage in
  `src/__tests__/unary-retry.test.ts` (a mutation is attempted exactly once on a retryable error;
  a read is retried; `RESOURCE_EXHAUSTED` is never retried; backoff varies between calls).
- **2026-08-18**: `checkPermissions`/`checkAny`/`checkAll` did not verify that `checkBulkPermissions`
  returned as many pairs as were requested — the result array came straight from `resp.pairs.map(...)`,
  and nothing compared its length to the request's. The proto guarantees pairs are returned in
  request order but says nothing about count, so a short response silently desynced `results[i]`
  from `checks[i]` for every item after the gap: one resource's answer attributed to another.
  `checkAll` then ran `.every()` over that short array and returned `true` where the dropped checks
  would have denied. The `checkPermissions` doc already promised "one per check request, in the same
  order" — a claim nothing enforced. A length mismatch in either direction now throws a
  `SpiceDBError` naming both counts. This is the same guard the other six clients received.
- **2026-08-18**: a malformed `CheckBulkPermissionsPair` — the `response` oneof set to neither `item`
  nor `error` — now throws a `SpiceDBError` instead of degrading to an `unspecified` `CheckResult`.
  `.map()` preserves index alignment so this case never caused the desync above, and `unspecified`
  is non-granting, so the old behavior was fail-closed rather than unsafe. It was changed anyway
  because an `unspecified` result is indistinguishable from a genuine "no permission" answer from
  the server, which hid a broken server behind a plausible-looking denial — and because it was the
  one remaining client out of seven that did not throw here.
- **2026-08-18**: **`watch()` mapped any unrecognized relationship-update operation — including
  `OPERATION_UNSPECIFIED` and any future wire value — to `"touch"`.** `"touch"` was the `switch`
  statement's `default` arm rather than a `case`, so an operation the client could not interpret
  was reported to the caller as a write. A cache or index mirror consuming the watch stream would
  upsert a relationship on an update it did not understand — one that may in fact have been a
  delete. `WatchChange["operation"]` gains a fourth member, `"unspecified"` (a type change, but
  this client is unreleased), `TOUCH` becomes an explicit `case`, and the `default` arm now yields
  `"unspecified"`. Root `DESIGN.md`, "RULE: A conversion that cannot preserve meaning must fail",
  clause 2: server-supplied values the client does not recognise MUST NOT raise, and MUST map to
  the safe, non-permissive default — never a grant, and never a write. Callers switching on
  `operation` must now handle `"unspecified"`; treat it as "re-read the relationship" or fail the
  mirror closed, never as a write.
- **2026-08-18**: `toProtoRelationshipFilter()` (used by `readRelationships`, `deleteRelationships`,
  `exportBulkRelationships`, `Transaction.mustMatch`/`mustNotMatch`, and
  `experimentalRegisterRelationshipCounter`) silently dropped `subjectId`/`subjectRelation` when
  `subjectType` was not set, instead of throwing. `optionalSubjectFilter` was only built inside
  `if (filter.subjectType)`, so `{ resourceType: "document", subjectId: "alice" }` produced a
  proto `RelationshipFilter` with **no subject constraint at all**, while the filter object itself
  still carried `subjectId: "alice"` — a caller reading the object back would see the constraint
  they set; the server would not. `deleteRelationships(cs, filter)` called with that filter
  deleted every relationship on every document, not just alice's — a correct-looking
  user-offboarding call that wipes the whole system. The wire's `SubjectFilter.subjectType` is a
  required field, so there is no way to express a subject ID/relation constraint without it,
  which makes silent widening the one unsafe resolution — `toProtoRelationshipFilter()` now
  throws `InvalidArgumentError` naming the field that was set without `subjectType`, per root
  `DESIGN.md` "RULE: A conversion that cannot preserve meaning must fail", clause 1
  (caller-supplied data the client cannot represent MUST raise a typed error). No signature
  change: the function already threw for other invalid inputs elsewhere in this file, and every
  call site already propagates synchronous throws (async generators throw on first `.next()`;
  `withRetry`'s `isTransientError` check correctly does not retry a `SpiceDBError`). No
  pre-existing test asserted the silent-drop behavior, so none needed replacing.

- **2026-08-18**: `checkAll()` returned `true` for zero checks —
  `Array.prototype.every` is vacuously `true` over an empty array. Root
  `DESIGN.md`'s "An aggregate over zero checks is not a grant" clause names
  the hazard: a gate like `checkAll(cs, "edit", ...docs.map(toCheck))` was
  silently granted whenever the derived checks array came up empty — a
  filter that matched nothing, an upstream returning `[]`. `checkAll` now
  guards the empty case before the aggregate and returns `false` without
  calling `checkBulkPermissions`. `checkAny` is unchanged — it was already
  correctly `false` on empty (`Array.prototype.some`).

- **2026-08-17**: A per-item error from `checkPermissions()`'s underlying
  `CheckBulkPermissions` call (a permission-denied, an invalid-argument, an
  internal server error, etc. scoped to one specific check) is now thrown as
  a typed `SpiceDBError` via the same code -> error-class mapping as a
  top-level RPC failure. Previously it was silently coerced into `false` —
  indistinguishable from a real denial, and the caller never learned an
  error occurred at all. New `toSpiceDBErrorFromStatus()` in `errors.ts`
  converts the `google.rpc.Status`-shaped per-item error (its numeric
  `code` matches Connect's `Code` enum, since both mirror the standard gRPC
  status codes) through the existing `toSpiceDBError()` mapping.

- **2026-08-14**: Enabled `stripInternal` in `tsconfig.json` so `@internal`-tagged
  members are actually removed from the shipped `.d.ts` (previously `@internal`
  JSDoc had no emit effect on its own). `Consistency._toProto()`/`_wrap()` and
  `Transaction.updates`/`preconditions`/`metadata` — along with their
  `@spicedb/proto` type imports — no longer appear in `dist/consistency.d.ts`
  or `dist/types.d.ts`. No public API change; these members were never
  intended to be public.

### Breaking Changes

- **2026-08-18** (behavioral; new option): per root DESIGN.md, "RULE: Credentials over insecure
  transport require an explicit opt-in" -- `createSpiceDBClient(..., { insecure: true })` (both
  the idiomatic client and the underlying proto client) now refuses to construct a client for a
  non-loopback endpoint (loopback means `localhost`, `127.0.0.0/8`, `::1`, or a `unix:` socket
  target). Previously an insecure connection would send its bearer token in cleartext to any host
  -- Connect-ES's request interceptor sets the `authorization` header unconditionally, regardless
  of transport security, so nothing checked where the connection actually went. A new option,
  `allowInsecureRemoteCredentials: true`, opts in explicitly when a caller genuinely means to send
  credentials in cleartext to a remote host; it must be passed alongside `insecure: true`, since
  neither alone is sufficient for a non-loopback endpoint anymore. `insecure: true` against
  `localhost` is unaffected -- no code change needed for local development. Thrown as a plain
  `Error`, before any HTTP/2 session or transport is created.

- **2026-08-18** (behavioral; no signature change): the two entries below change what existing,
  unmodified call sites do. They are listed here because neither announces itself -- nothing
  fails to compile, and the difference only shows up under load or against a slow query.
  - **Unary calls are now bounded by a 30-second default** -- see "Call deadlines" in this
    release. A call that legitimately takes longer than 30 s (most plausibly a deep
    `expandPermissionTree` on a large graph, or a filtered delete sweeping many pages) now fails
    with a deadline error where it previously ran to completion. Raise it with
    `createSpiceDBClient(..., { defaultTimeoutMs })`, or pass `timeoutMs` on the individual call.
    There is deliberately no way to ask for no bound at all on a unary call.
  - **Mutations are no longer retried automatically** -- see "Retry safety" in this release.
    `write`, `deleteRelationships`, `writeSchema`, `importBulkRelationships`, and the experimental
    counter register/unregister calls now surface a transient `UNAVAILABLE` to the caller on the
    first attempt rather than retrying. This is the correct default (replaying a non-idempotent
    write can report failure for a write that in fact committed), but a caller who was relying on
    the client to ride out a rolling restart must now retry themselves, knowing their own
    idempotency. Reads are unaffected. `RESOURCE_EXHAUSTED` is no longer retried either, on reads
    or mutations.

- **2026-08-17**: `checkPermission()` now returns `CheckResult` instead of
  `boolean`, and `checkPermissions()` now returns `CheckResult[]` instead of
  `boolean[]` — closing a fail-open. Previously, both methods collapsed
  `HAS_PERMISSION` and `CONDITIONAL_PERMISSION` together into `true`
  (`resp.permissionship === HAS_PERMISSION || resp.permissionship ===
  CONDITIONAL_PERMISSION`), so a caveated relationship whose context was not
  supplied at check time was granted exactly as if the server had confirmed
  it — this client's own JSDoc documented it as intentional
  ("Caveated permissions return `true`"). `CheckResult` — a class, so
  `hasPermission()` travels with the data — carries `permissionship`
  (`Permissionship`, now with a fourth value, `"noPermission"`, alongside
  `"unspecified"` | `"hasPermission"` | `"conditionalPermission"`),
  `missingContext: string[]` (from `partial_caveat_info`), `checkedAt:
  string` (from `checked_at`), and `hasPermission(): boolean` — `true` ONLY
  for `permissionship === "hasPermission"`. `checkAny()`/`checkAll()` keep
  returning `boolean` but now count only `hasPermission() === true` results
  as granted; a conditional result never counts, even for `checkAny()`. This
  is the TypeScript instance of a change applied identically across all
  seven SpiceDB clients; mirrors spicedb-go's `CheckResult`
  (`client/check_types.go`).

  Before:
  ```ts
  const allowed = await client.checkPermission(cs, check);
  if (allowed) grant(); // conditional (caveat context missing) ALSO ran this — the fail-open

  const results = await client.checkPermissions(cs, ...checks);
  if (results[0]) grant();
  ```
  After:
  ```ts
  const result = await client.checkPermission(cs, check);
  if (result.hasPermission()) grant(); // false for a conditional result — closed

  const results = await client.checkPermissions(cs, ...checks);
  if (results[0].hasPermission()) grant();
  // A conditional result also carries what's missing and when it was checked:
  if (result.permissionship === "conditionalPermission") {
    console.log("missing caveat context:", result.missingContext);
  }
  ```

- **2026-08-15**: `lookupResources`/`lookupSubjects` now yield native result
  objects instead of bare `string` IDs, closing an over-grant risk: the
  previous `string`-only shape silently dropped `excludedSubjects` for
  wildcard (`user:*`) matches, so a caller iterating IDs alone could treat a
  wildcard-excluded subject as granted. `lookupResources` now yields
  `LookupResource` (`resourceId`, `permissionship`, `partialCaveat?`);
  `lookupSubjects` now yields `LookupSubject` (`subject: ResolvedSubject`,
  `excludedSubjects: ResolvedSubject[]`). Both use the shared `Permissionship`
  (`"unspecified" | "hasPermission" | "conditionalPermission"`) and
  `PartialCaveatInfo` types. Mirrors spicedb-go's
  `client/lookup_types.go`/`lookup.go`, including its fallback to the
  deprecated `subjectObjectId`/`excludedSubjectIds` proto fields for servers
  that don't yet populate the modern `subject`/`excludedSubjects` fields.
  All new types are exported from the package root.

  Before:
  ```ts
  for await (const resourceId of client.lookupResources(params, cs)) {
    grant(resourceId); // string only — no permissionship signal
  }
  for await (const subjectId of client.lookupSubjects(params, cs)) {
    grant(subjectId); // wildcard "*" treated as unconditional — over-grant risk
  }
  ```
  After:
  ```ts
  for await (const resource of client.lookupResources(params, cs)) {
    if (resource.permissionship !== "hasPermission") continue; // skip conditional
    grant(resource.resourceId);
  }
  for await (const result of client.lookupSubjects(params, cs)) {
    const excluded = new Set(result.excludedSubjects.map((s) => s.subjectId));
    if (result.subject.subjectId === "*" && excluded.has(callerId)) continue;
    grant(result.subject.subjectId);
  }
  ```
- **2026-08-14**: Removed `@bufbuild/protobuf`'s `JsonObject` from the public
  API. `Relationship.caveatContext`, `CheckRequest.context`,
  `LookupResourcesParams.context`, `LookupSubjectsParams.context`,
  `WatchEvent.metadata`, and `Transaction.withMetadata()` now use the native
  `Record<string, unknown>` type instead. No call-site changes are required
  for plain object literals; only code that explicitly imported `JsonObject`
  from `@bufbuild/protobuf` to type these values needs to switch to
  `Record<string, unknown>`.

  Before:
  ```ts
  import type { JsonObject } from "@bufbuild/protobuf";
  const ctx: JsonObject = { key: "value" };
  ```
  After:
  ```ts
  const ctx: Record<string, unknown> = { key: "value" };
  ```
- **2026-08-14**: `expandPermissionTree`, `reflectSchema`, `diffSchema`,
  `computablePermissions`, and `dependentRelations` now return fully-typed
  native structures instead of `unknown`/`unknown[]` proto leakage.
  `expandPermissionTree`'s `treeRoot` is now a native `PermissionTree`
  (mirrors spicedb-go's `PermissionTree`/`IntermediateNode`/`LeafNode`/
  `ObjectRef`/`SubjectRef`/`TreeOperation`); `reflectSchema`'s `definitions`/
  `caveats` are now `SchemaDefinition[]`/`SchemaCaveat[]`; `diffSchema`'s
  `diffs` are now `SchemaDiff[]`; `computablePermissions`/`dependentRelations`
  continue to return `RelationReference[]`, now built via a shared mapper.
  All new types are exported from the package root.

  Before:
  ```ts
  const { treeRoot } = await client.expandPermissionTree(cs, params);
  const objId = (treeRoot as any).expandedObject.objectId; // unknown, required casting
  ```
  After:
  ```ts
  const { treeRoot } = await client.expandPermissionTree(cs, params);
  const objId = treeRoot.expandedObject.objectId; // fully-typed PermissionTree
  ```
- **2026-08-14**: `Consistency` is now an opaque native class instead of a
  re-exported protobuf-es type. `full()`, `minLatency()`, `atLeast()`,
  `snapshot()`, `atLeastOrFull()`, and `atLeastOrMinLatency()` now return the
  native `Consistency` class; the underlying proto message is no longer part
  of the public API. All `SpiceDBClient` methods that accept `consistency`
  unwrap it internally via an `@internal` `_toProto()` method before building
  the proto request. No call-site changes are required — construct consistency
  values only via the exported helper functions, never directly.

  Before:
  ```ts
  import type { Consistency as ProtoConsistency } from "@spicedb/proto";
  const cs: ProtoConsistency = full()._toProto(); // reaching into internals
  ```
  After:
  ```ts
  const cs = full(); // opaque Consistency; pass directly to client calls
  ```

## 0.1.0 (2026-03-16)

Initial release of the idiomatic TypeScript SpiceDB client.

### Features

- **2026-03-16**: Initial implementation of the idiomatic TypeScript client.
  Created `src/client.ts`, `src/types.ts`, `src/consistency.ts`, `src/errors.ts`,
  `src/index.ts`. Full API coverage for all non-deprecated proto APIs:
  PermissionsService (checkPermission, checkPermissions, checkAny, checkAll,
  readRelationships, write, deleteRelationships, lookupResources,
  lookupSubjects, expandPermissionTree, importBulkRelationships,
  exportBulkRelationships), SchemaService (readSchema, writeSchema,
  reflectSchema, computablePermissions, dependentRelations, diffSchema),
  WatchService (watch), and ExperimentalService relationship counters
  (experimentalRegisterRelationshipCounter, experimentalCountRelationships,
  experimentalUnregisterRelationshipCounter). Added 8 examples covering all
  major use cases. Added experimental naming convention to DESIGN.md.
