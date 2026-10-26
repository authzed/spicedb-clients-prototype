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
    `content` array; the default includes relationships only.
  - `TransportSecurity.Tls/Plaintext` and
    `RemoteCredentials.LoopbackOnly/AllowPlaintext` express connection policy.
  - Validate enum values at runtime before issuing RPCs. Booleans remain
    appropriate for result facts such as `hasPermission()` and `isCheckpoint`.

## Connections and operations

- **Make security and ownership explicit at construction.**
  - `new SpiceDBClient({ endpoint, token, transportSecurity?, remoteCredentials?,
    tls?, headers?, maxRetries?, defaultTimeoutMs? })` defaults to TLS.
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
    `timeoutMs`. Check options add context defaults and the tracing enum.
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
  - Retry only safe reads on unavailable/aborted, at most three times with
    full-jitter backoff starting at 100 ms. Never replay mutations or consumed streams.

- **Release streams and uploads deterministically.**
  - Iterator `return()`/`throw()`, `for await` exit, abort and client closure
    terminate the underlying call, including while `next()` is pending.
  - Imports accept sync/async iterables with bounded batches and backpressure.
    Producer/conversion failures cancel the upload; abort interrupts blocked producers.
  - Read/resource lookup pagination enforces limits and advancing cursors.
    Returned data is immutable and includes each response's actual revision.

- **Keep filtering and deletion scope exact.**
  - Subject relation selectors distinguish Any, Direct and Named via a
    discriminated union; unrepresentable filters fail instead of widening.
  - `deleteRelationships(filter, { limit, ... })` performs one bounded request.
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

- **Verify the public contract and real transport behavior.**
  - Run build/unit checks, example typechecking and API extraction; regenerate
    schema fixtures and run positive/negative compilation plus live examples.
  - Assert token identity/serialization, enum wire mappings/defaults/rejection,
    remote opt-in acceptance, nested snapshots and real singular tracing.
  - Observe actual stream release and successful TLS/mTLS handshakes, and
    retain error, retry, deadline and malformed-response regressions.

- Generated subject lookups use the selector for the subject type.
  - The optional `subjectRelation` is a native relation name validated by the server, rather than a schema-constrained generated selector.
