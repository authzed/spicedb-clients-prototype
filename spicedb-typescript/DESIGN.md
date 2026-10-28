# TypeScript client design

This document applies the [shared design](../DESIGN.md) to the TypeScript client
and its generated schema facade.

## Public API and values

- **Use native, typed request and result objects.**
  - The [root design](../DESIGN.md) defines shared semantics, RPC coverage, security, and verification.
  - Export the client, domain values, enums, consistency factories and errors
    from `@spicedb/client`. Ordinary operations do not require protobuf types.
  - `ObjectRef` contains `objectType`/`objectId`; `SubjectRef` contains an object
    and optional userset relation. `Relationship` holds persisted caveat and
    expiration data; `CheckRequest` holds permission and evaluation context.
  - Immutable boundaries snapshot nested JSON, arrays and dates. Context keys
    are strings; nulls are preserved and unsupported values report their path.
  - Use discriminated unions for exclusive filter, permission-tree and trace
    variants. Unknown wire states remain explicit and never grant permission.

- **Represent revisions with an immutable runtime `ZedToken` class.**
  - `new ZedToken(value)` retains an opaque string; `toString()` and `toJSON()`
    serialize it losslessly. `equals(other)` checks token value equality. The
    representation is private and the instance frozen.
  - Every public revision input/result uses `ZedToken`, including checks, schema,
    watch, deletion, counters and materialize cursors. Authentication tokens and
    ordinary opaque pagination cursors remain strings.
  - An empty wire revision remains an empty token value; token-based consistency
    rejects empty values. Absent optional tokens remain `undefined`.
  - `Consistency` is immutable. `full()`, `minLatency()`, `atLeast(token)` and
    `snapshot(token)` are explicit strategies; optional-token helpers name their fallback.

- **Expose discoverable behavioral choices through enums.**
  - `LookupPermissionshipFilter.All/HasPermissionOnly` selects all resource lookup
    results or only unconditional grants. Filtering happens before yielding;
    limits count matches and paging advances through skipped results. Examples
    listing accessible IDs select `HasPermissionOnly`; typed facades forward it.
  - `SubjectRelationKind.Any/Direct/Named` tags the exact-filter discriminated union.
  - `Tracing.Disabled/Enabled` controls singular and bulk check tracing.
  - `LookupDebug.Disabled/Enabled` controls lookup diagnostics;
    `Wildcards.Default/Include/Exclude` controls wildcard subject results.
  - `watch({ relationshipFilters })` accepts native relationship filters with OR
    semantics and snapshots them before iteration. Examples use filters; nonempty
    object types and relationship filters cannot be combined.
  - `WatchContent.Relationships/Checkpoints/Schema` values form a readonly
    `content` array; the default includes relationships only. `Schema` combines
    with `Checkpoints`, and events report `schemaUpdated` separately from
    relationship updates.
  - `TransportSecurity.Tls/Plaintext` and
    `RemoteCredentials.LoopbackOnly/AllowPlaintext` express connection policy.
  - Validate enum values at runtime before issuing RPCs. Booleans remain
    appropriate for result facts such as `hasPermission()` and `isCheckpoint`.

## Connections and operations

- **Make security and ownership explicit at construction.**
  - `new SpiceDBClient({ endpoint, token, transportSecurity?, remoteCredentials?,
    tls?, headers?, retryPolicy?, retries?, streamRetries?, defaultTimeoutMs?,
    defaultDeadline? })` defaults to TLS.
  - `SpiceDBClient.fromTransport(transport, behaviorOptions?)` wraps a
    caller-owned Connect `Transport` (an in-memory `createRouterTransport`, an
    HTTP/2 transport the caller configured, a test server's). Connection options
    (`endpoint`, `token`, `tls`, `headers`, transport security) are rejected
    there because the transport carries them; behavior options apply. `close()`
    cancels the client's operations and never closes the supplied transport.
  - Remote plaintext is supported through the normal constructor and
    `createSpiceDBClient`: select `TransportSecurity.Plaintext` and explicitly
    set `RemoteCredentials.AllowPlaintext`. The loopback-only policy is the default.
  - Retain endpoint-parser guards and custom CA/mTLS support. Reject TLS material
    with plaintext. Copy caller headers and preserve auth on every service call.
  - `raw()` returns the same authenticated protocol client. `close()` is
    idempotent and cancels active operations before closing its owned transport.

- **Use one extensible signature per operation.**
  - `write` returns an immutable `WriteResult` with a `revision: ZedToken`.
    Generated `touch`, `create` and `delete` return that same result.
  - Requests describe the operation; `CallOptions` supplies `signal` and
    `timeoutMs`. Check options add context defaults, the tracing enum and
    `itemErrors`.
  - `CallOptions` is also the transport escape hatch on every operation:
    `headers` (per-call request headers, applied after the client's own),
    `onHeader` and `onTrailer` (response metadata) are forwarded to every RPC
    the operation makes, so they may fire once per attempt, chunk or page. This
    is the supported route to data that SpiceDB versions before 1.30 send in
    trailers; it needs no `raw()` client. SpiceDB 1.30+ is the native floor.
  - Unary methods return promises. Streaming methods return
    `AsyncIterableIterator<T>` directly; never a promise of an iterator.
  - Checks, reads, lookups, expansion and schema inspection require explicit
    consistency. Bulk checks accept readonly collections and reject mixed strategies.
  - Preserve every nondeprecated RPC, including schema reflection/diff,
    import/export, watch, relationship counters and materialize services.

- **Preserve authorization meaning and diagnostics.**
  - `check(request, options?)` calls the singular RPC. `checkBulk(requests,
    options?)` sends at most 1,000 items per chunk and validates echoed requests.
  - `CheckResult` retains permissionship, missing context, typed revision,
    optional expiration and native recursive debug trace. Only unconditional
    permission makes `hasPermission()` true; JavaScript object truthiness is unsafe.
  - Tracing is disabled by default and forwarded to each bulk chunk. Preserve
    schema, recursive subproblems, cached false presence, native caveat JSON,
    nanosecond duration, timestamps, operation IDs and sources. Traces never alter grants.
  - Merge check context by key with per-item values overriding defaults; preserve
    original server errors with their absolute bulk input index.

## Lifetime, retries and deletion

- **Share one deadline and cancellation signal across each operation.**
  - Unary calls default to a 30,000 ms total timeout, including retries,
    backoff, chunks and pages. Streams/imports have no default lifetime limit.
  - `AbortSignal` works during pending RPCs, backoff, producer waits and between
    chunks. Preserve native abort identity and the caller's reason.
  - `DEFAULT_RETRY_POLICY` retries idempotent reads on Unavailable/Aborted, at
    most three times, with full-jitter backoff starting at 100 ms. A client-wide
    `retryPolicy: { maxRetries, retryableCodes, initialBackoffMs, maxBackoffMs?,
    onRetry? }` replaces it (callers may opt into other read codes such as
    ResourceExhausted); `retries: Retries.Disabled` makes every operation one
    attempt; `streamRetries: StreamRetries.Disabled` opens each server stream
    once. `CallOptions.retryPolicy` overrides the client's policy for one call,
    including stream establishment (so one stream can be exempted, and a
    `watch`, opened once by default, can opt in). Mutations are never replayed
    automatically: only a touch-only `write` without preconditions and
    `writeSchema` may opt in per call, and every other mutation rejects a
    policy with retries (`InvalidArgumentError`, before any request). Consumed
    streams never restart.
  - `defaultDeadline: DefaultDeadline.Disabled` removes the 30 s default budget
    (combining it with `defaultTimeoutMs` is rejected); a call's own
    `timeoutMs`/`signal` still apply. No zero or negative magic values.

- **Release streams and uploads deterministically.**
  - Iterator `return()`/`throw()`, `for await` exit, abort and client closure
    terminate the underlying call, including while `next()` is pending.
  - Imports accept sync/async iterables with bounded batches
    (`ImportOptions.batchSize`, default 1,000) and backpressure. One call is one
    stream and one transaction. When the server aborts the stream (for example
    ALREADY_EXISTS on a duplicate) the server's final status is reported, never
    a transport end-of-stream. Producer/conversion failures cancel the upload;
    abort interrupts blocked producers.
  - Paged reads repeat the caller's consistency and one fixed page size on every
    page: SpiceDB hashes both into the cursor (which pins later pages to the first
    page's revision) and rejects a cursor sent with different arguments. Totals
    (`limit`) are enforced client-side, never by shrinking a page.
    `readRelationships` yields `ReadRelationship` (the relationship's fields plus
    `afterResultCursor` and `readAt`), `exportRelationships` yields
    `ExportedRelationship` (plus its batch's `afterResultCursor`; exports carry no
    read-at), and `LookupResource` carries `afterResultCursor` and `lookedUpAt`.
    Resume by passing a cursor as the request's `cursor` with the same request and
    page size. Returned data is immutable. `paging: Paging.SingleRequest` on
    `readRelationships`/`lookupResources` sends exactly one request like the
    raw API: its limit is `limit` (or unbounded), `pageSize` is ignored, and no
    follow-up page is fetched.

- **Keep filtering and deletion scope exact.**
  - Subject relation selectors distinguish Any, Direct and Named via a
    discriminated union; unrepresentable filters fail instead of widening.
  - `deleteRelationships(filter, options)` performs one request. `DeleteOptions`
    is a discriminated union: the default `DeletePartial.Allowed` requires a
    positive `limit`; `partial: DeletePartial.Disallowed` is all-or-nothing (the
    server rejects a delete over the limit) and `limit` may be omitted for the
    server default. `DeleteAllOptions.onProgress` reports the cumulative result
    after each acknowledged batch.
    `deleteAllRelationshipsNonAtomic(filter, { pageSize?, ... })` explicitly continues,
    defaults to 1,000 per page and shares a total budget. Each batch is atomic;
    the overall operation is not. Completed batches remain committed on failure.
  - Forward each non-empty deletion cursor unchanged to the next batch; omit
    absent/empty cursors. Failure progress retains the last acknowledged cursor.
  - Delete results carry typed revision, count, completion and optional cursor.
    Unknown completion is an error. Errors retain cumulative acknowledged progress;
    unacknowledged mutations are never automatically replayed.

## Experimental services and generated facades

- **Expose materialize results as native values.**
  - Cover roaring lookup, count-by-filter, permission-set watch/lookup and
    download manifests on the same authenticated transport.
  - Permission-set events preserve all response/change variants. Their cursor
    preserves limit, typed token, starting index/key, completed-members state and cursor.
  - Permission-set lookup supports bounded paging and total limits. Download
    returns file metadata and timestamps; it does not fetch referenced URLs.
  - Deprecated WatchPermissions remains available through raw protocol access.

- **Keep generated schema APIs thin and fully option-aware.**
  - Every public native capability is directly available on the generated client.
    Schema-aware check/lookup signatures retain their constraints; deletion,
    watch, schema, import/export, counters and materialize forward native types.
  - Lookup/read options expose the complete native option set. Forwarders preserve
    promise/iterator identity and error progress; a compile-time parity check
    catches future missing methods.
  - Generated checks forward tracing, timeout and abort; connections forward
    transport and remote-credential enums. Return native `ZedToken`/check results.
  - Collection writes and directly returned async iterators retain ownership and
    cancellation behavior. Generated caveat contexts use native JSON types.
  - `TypedTransaction` exposes `updates`, `preconditions` and `error`;
    `withSubject` always constrains the subject relation and
    `withSubjectAnyRelation` does not. `--typescript.rename` keeps schemas
    whose names collide with generated identifiers (see
    `spicedb-gen/DESIGN.md`).

- **Verify the public contract and real transport behavior.**
  - Run build/unit checks, example typechecking and API extraction; regenerate
    schema fixtures and run positive/negative compilation plus live examples.
  - Assert token identity/serialization, enum wire mappings/defaults/rejection,
    remote opt-in acceptance, nested snapshots and real singular tracing.
  - Observe actual stream release and successful TLS/mTLS handshakes, and
    retain error, retry, deadline and malformed-response regressions.

- Generated subject lookups use the selector for the subject type.
  - The optional `subjectRelation` is a native relation name validated by the server, rather than a schema-constrained generated selector.

## Adoption items: tooling, tests and migration

Each root DESIGN.md requirement below names the TypeScript API that satisfies it.

- **Single protocol copy / legacy bridge.** Not applicable as a conflict:
  protobuf-es v2 has no process-global descriptor registry (descriptors are
  values and registries are explicit), and `@authzed/authzed-node`'s
  protobuf-ts runtime has none either, so both load in one process and one
  bundle. `proto-clients/spicedb-typescript-proto/src/__tests__/legacy-coexistence.test.ts`
  loads both and exchanges messages over the wire format. No bridge generator
  is needed; `MIGRATION.md` covers moving off authzed-node.
- **Existing-connection constructor.** `SpiceDBClient.fromTransport` (above).
- **Per-call transport options and response metadata.** `CallOptions.headers`,
  `onHeader`, `onTrailer` on every operation (above).
- **Exported protocol conversions.** The `protoconv` namespace exports
  `*ToProto`/`*FromProto` for objects, subjects, relationships, filters,
  preconditions, updates, consistency, tokens, check requests, check results
  with debug traces, lookups (resources and subjects), expand results, read
  items and write results. Response conversions reproduce the server's wire
  shape: `checkBulkToProto(requests, results, defaultContext?)` rebuilds a whole
  bulk-check response (per-item errors as statuses, `checkedAt`) with
  `checkBulkRequestItemToProto` for its items, and `lookupSubjectToProto`
  fills the deprecated top-level fields SpiceDB still sends.
- **Server acceptance rules.** `write` sends empty transactions (SpiceDB
  checks their preconditions and returns the current revision). `watch`
  rejects `objectTypes` combined with `relationshipFilters` because SpiceDB
  does. Every read that takes a consistency requires one, schema reads
  included; `minLatency()` matches the raw API's default.
- **1.30+ floor.** Pre-1.30 trailer data is reachable through `onTrailer` only.
- **Error status interop, constructor, verbatim message.** Every `SpiceDBError`
  has `code` (Connect's `Code`, matching `ConnectError.code`), including local
  validation (`InvalidArgument`), client deadlines (`DeadlineExceeded`) and
  caller-built errors. `toConnectError()` returns the server's original
  `ConnectError` (metadata, details) or a new one with the same code; the free
  `toConnectError(err)` and `errorCode(err)` also accept raw `ConnectError`s and
  abort/timeout `DOMException`s, and `SpiceDBError.from(err)` maps any of them to
  the native subclass. `SpiceDBError.fromCode(code, message)` builds the same
  subclass a server error with that code produces, for test doubles.
  Wrapper errors are the inner error itself: a bulk item error (prefixed
  `check item N:`) and an error carrying `deleteProgress` keep the server's
  code and `serverMessage`, so `toConnectError(err).rawMessage` and
  `errorCode(err)` report the server's status once, without the prefixes.
  `serverMessage` holds the server's status message verbatim; `message` keeps
  Connect's `[code]` prefix and any operation context. `SpiceDBError` cannot
  subclass `ConnectError`: Connect's `Symbol.hasInstance` accepts only its own
  prototype or the name "ConnectError", so a subclass with its own name fails
  `instanceof ConnectError` (and `ConnectError.from(native)` reports Unknown).
  Use `errorCode`/`toConnectError` instead.
- **Filter matching.** `filterMatches(filter, relationship)` applies server
  semantics, including Any/Direct/Named subject relations.
- **In-memory test double.** `InMemorySpiceDB` implements `write` (atomic, with
  preconditions and CREATE conflicts; empty writes return the revision),
  `readRelationships` (cursors and limits), `deleteRelationships` (bounded and
  all-or-nothing), `deleteAllRelationshipsNonAtomic`, `importRelationships`
  (one transaction; duplicates fail), `exportRelationships` (filter and
  cursor), `readSchema`, `writeSchema`, `reflectSchema` (result registered with
  `setReflectSchemaResult`), `check`, `checkBulk` (including
  `BulkItemErrors.Returned`) and `close` with the client's signatures, so
  `Pick<SpiceDBClient, ...>` accepts both. Checks return results or errors
  registered with `setCheckResult`; unregistered checks deny. Its errors have
  the real client's classes, codes and message shape (server failures carry
  Connect's `[code]` prefix and `serverMessage`; local validation does not),
  it rejects the same retry opt-ins, and it reports `setResponseMetadata`
  values to `onHeader`/`onTrailer`. Options are plain inspectable values, so no
  option-resolving API is needed.
- **Panicking constructors.** Not applicable: TypeScript constructors and
  factories (`relationship`, `relationshipFromTuple`, `new ZedToken`) already
  throw, and plain object literals need no constructor.
- **String/log/serialization forms and transaction length.**
  `relationshipToString`, `objectRefToString`, `subjectRefToString`,
  `filterToString`, `updateToString`, `preconditionToString`;
  `Transaction.length`, `updates`, `preconditions`, `toString()`, `toJSON()` and
  Node's inspector; `Consistency.toString()`/`toJSON()`. Native relationships,
  filters and results are plain readonly data, so `JSON.stringify` and loggers
  already print their fields.
- **Short relationship accessors.** Not applicable: `Relationship` is a plain
  readonly interface, so `rel.resource.objectId` and `rel.subject.object.objectId`
  are property paths, not getter chains, and a getter class would break object
  literals and spreads used throughout the API.
- **Full tuple syntax and copy helpers.** `relationshipFromTuple(text)` and
  `relationshipToString(rel)` cover `[caveat:{...}]` and `[expiration:...]`
  (milliseconds; finer digits are truncated) and are tested against SpiceDB's own
  formatted output. Copies use object spread (`{ ...rel, subject }`); writes drop
  any extra fields such as paging state.
- **Bulk item-error mode.** `checkBulk(requests, { itemErrors:
  BulkItemErrors.Returned })` returns every item; failures are on
  `CheckResult.error` (with `inputIndex`) with `"unspecified"` permissionship.
- **Default-deadline disable, retry policy, stream retries, import status and
  batch size, paging state, watch schema changes, all-or-nothing delete and
  delete-all progress.** See the sections above.
- **MIGRATION.md and dependency floors.** `MIGRATION.md` covers
  `@authzed/authzed-node`. Dependencies declare the lowest versions verified to
  work (`@bufbuild/protobuf` ^2.11.0, matching the pinned protoc-gen-es;
  `@connectrpc/connect` ^2.0.0); `@types/node` is an optional peer (>=20.19.0).
  Build and test tools stay in devDependencies.
- **AI integration guide.** `AI_INTEGRATION_GUIDE.md` shows the current API;
  its snippets are type-checked against the generated sample fixture.
