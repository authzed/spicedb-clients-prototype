# SpiceDB Go client design

- **Use native Go packages and explicit domain values.**
  - The [root design](../DESIGN.md) defines shared semantics, RPC coverage, security, and verification.
  - `client` owns operations, requests, results, functional options, and typed errors; `rel` owns immutable references, relationships and filters plus mutable transaction builders; `consistency` owns strategies and tokens.
  - `consistency.ZedToken` is an opaque value struct with private storage. `NewZedToken(string)` and `String()` preserve its exact contents. Its zero value represents absence. Public revision inputs and outputs use this type; cursors remain separate.
  - `Full`, `MinLatency`, `AtLeast`, and `Snapshot` specify consistency. Every consistency-bearing read requires a strategy; only `AtLeastOrFull` and `AtLeastOrMinLatency` explicitly fall back for an absent token.
  - Persisted relationships contain tuple data, caveats and expiration. Permission questions use `PermissionCheck`, including check context. Values and options snapshot caller-owned nested data.

- **Provide one extensible operation surface.**
  - Operations accept context, a native request, and trailing operation-specific functional options. Bulk checks take a slice; incremental checks and streams use Go iterators.
  - Behavioral choices use named enums: `CheckTracing`, `LookupDebug`, `WatchCheckpoints`, `RemotePlaintextCredentials`, and `rel.PreconditionOperation`. Unknown values fail with `ErrInvalidArgument` before transport use. Binary result facts remain booleans.
  - Every supported non-deprecated RPC has a native wrapper; `API_COVERAGE.md` records coverage. Counter and materialize operations are experimental. Protocol access is secondary through `RawProto`, which shares authentication and ownership; `WithCallOptions`/`WithResponseMetadata` and `client/protoconv` cover transport data and protocol-shaped output without it.

- **Make check results safe and diagnostic data complete.**
  - Only unconditional `HasPermission` grants. Conditional, denied and unknown states remain distinguishable. Results preserve missing context, native revision and singular expiration.
  - Singular and bulk tracing default to `TracingDisabled`; `WithCheckTracing(TracingEnabled)` enables complete native recursive traces. Lookup debug uses its own `WithLookupResourcesDebug(DebugEnabled)` option.
  - Bulk requests use chunks of at most 1,000, validate common consistency and ordered echoed requests, and retain absolute failure indices and per-chunk revisions. `WithBulkItemErrors(ItemErrorsReturned)` returns per-item errors on `CheckResult.Err` (never a grant) instead of failing the call. Context merges by key with item values winning; invalid JSON fails with field information.

- **Apply one operation budget and deterministic cancellation.**
  - Unary defaults are 30 seconds (`WithDefaultDeadline(DefaultDeadlineDisabled)` removes the default; the caller's context still applies); explicit positive timeouts cover attempts, backoff, chunks and pages. Caller deadlines win. Streams and imports have no default lifetime deadline.
  - By default, idempotent reads retry only Unavailable and Aborted, at most three retries, with full-jitter exponential backoff starting at 100 milliseconds; `WithRetryPolicy`, `WithRetries(RetriesDisabled)` and `WithStreamRetries(StreamRetriesDisabled)` change that client-wide, and `WithCallRetryPolicy[O]` overrides it for one call (including stream establishment). Mutations are never replayed automatically; only touch-only `Write` calls without preconditions and `WriteSchema` may opt in per call, and every other mutation rejects the opt-in; streams never restart after exposing an item.
  - Iterator exit, exhaustion, failure and cancellation release transport. Imports consume incrementally in 1,000-item batches (`WithImportRelationshipsBatchSize`; one call is one transaction), report the server's final status when it aborts the stream, and cancel active uploads on producer or conversion failures. Native context cancellation identity is preserved.

- **Preserve exact filter scope and acknowledged deletion progress.**
  - Subject relation filters distinguish `AnyRelation`, `DirectRelation`, and `NamedRelation`. Exact relationship filters retain subject relation identity; unrepresentable constraints fail.
  - `DeleteRelationships` sends one request; it requires a positive total limit unless `WithDeletePartial(PartialDisallowed)` makes it all-or-nothing, where zero means the server default. `WithDeleteAllProgress` reports each acknowledged batch. `DeleteAllRelationshipsNonAtomic` uses an independent positive page size (default 1,000), one budget and per-page preconditions.
  - Results preserve revision, count, completion and cursor. Unknown completion or zero-progress partial responses fail. `DeleteProgressError` retains acknowledged cumulative progress and its cause, including cancellation; lost responses are never counted or replayed.

- **Make completion deletion explicitly non-atomic.**
  - `DeleteAllRelationshipsNonAtomic` commits each batch transactionally. The whole operation is non-atomic: completed batches remain committed if a later batch fails.
  - `DeleteResult.Revision` carries the native revision; failure progress preserves the last acknowledged revision and cumulative count.
  - Each nonempty server cursor becomes the next batch cursor. Empty or absent cursors retain the filter-based fallback; results and failure progress retain the last acknowledged cursor.

- **Preserve native stream and schema data.**
  - Reads, lookups and exports support limits and page sizes without confusing their meanings. Lookup subjects retains wildcard exclusions and conditional context. Paging repeats the caller's consistency and page size on every page (SpiceDB hashes both into the cursor, which pins the revision); items carry `AfterResultCursor` (and `ReadAt` for reads) and resume via `With*Cursor` with the same request and page size. `With{ReadRelationships,LookupResources}Paging(PagingSingleRequest)` sends one request like the raw API.
  - Watch accepts a native starting token, `WithUpdatesCheckpoints(CheckpointsIncluded)` and `WithUpdatesSchemaChanges(SchemaChangesIncluded)`; events retain update data, changes-through revision, checkpoint and schema-updated facts.
  - Schema reflection/diff, tagged permission trees, counters and materialize operations preserve structured native variants, revisions, safe unknown states and optional values. Materialize cursors retain all fields; download URLs are returned without fetching them.

- **Expose secure, configurable constructors.**
  - `NewPlaintext`, `NewSystemTLS` and `NewWithOpts` all accept functional options. TLS uses the system trust source; `WithDialOptions` supports custom trust and mTLS and documents override behavior.
  - Plaintext credentials to remote hostnames or IPs require `WithRemotePlaintextCredentials(RemotePlaintextAllowed)`. The default is `RemotePlaintextRejected`; loopback and local sockets work without the opt-in. `WithInsecure` selects plaintext for configurable constructors.
  - Endpoint validation uses the protocol transport parser and fails closed. Unknown policy values are invalid. Arbitrary dial overrides remain caller-controlled; credentials retain transport guards.
  - `Close` is idempotent. Native errors preserve status, causes, reason, domain and metadata; `errors.Is` and `errors.As` work for status, item and progress errors.
  - `*Error` implements `GRPCStatus`: a server error returns the server's status (details included); any other error returns a status built from `Code` and its message. `CodeOf` classifies `*Error`, raw status and context errors. `ServerMessage` holds the server's message verbatim; `Message` adds the operation. `NewError` builds errors for test doubles.

- **Serve tooling without the raw escape hatch.**
  - `NewFromConn` wraps a caller-owned `grpc.ClientConnInterface`; transport and credential options are rejected there, operation options apply, and `Close` never closes it.
  - `WithCallOptions[O]`/`WithResponseMetadata[O]` are generic over every operation option type; options apply to every attempt, chunk and page.
  - Retry behavior is client-wide: `RetryPolicy` (default three retries of Unavailable/Aborted, 100ms full-jitter), `WithRetries(RetriesDisabled)`, `WithStreamRetries(StreamRetriesDisabled)`. Mutations never retry. `WithDefaultDeadline(DefaultDeadlineDisabled)` removes the default budget.
  - Paged reads yield `ReadRelationship`/`ExportedRelationship` (embedding `rel.Relationship`) and `LookupResource` with cursors; every page repeats the caller's consistency and page size, because SpiceDB hashes both into the cursor.
  - `client/protoconv` exports conversions; relationship-level conversion lives in `internal/conv`, shared with the client.
  - Imports report the server's final status on an aborted stream. Bulk checks can return per-item errors (`ItemErrorsReturned`). Watch can request schema changes. Deletes can be all-or-nothing and delete-all reports per-batch progress.
  - `rel.FromTuple`/`String` implement SpiceDB's full tuple syntax; `spicedb-gen/tuplecompat` checks them against SpiceDB's parser.

- **Make native values observable and testable.**
  - `rel` values implement `String`, and relationships, updates, filters and preconditions implement `MarshalJSON` and `slog.LogValuer` as diagnostic forms (there is no `UnmarshalJSON`). `Txn.Len` avoids copying.
  - `Filter.Matches` applies server filter semantics client-side. `ErrFilter` carries a construction error that `Validate` reports, so typed builders fail closed instead of widening scope. `Must*` constructors exist for constants and tests.
  - `client/clienttest.Store` is an in-memory double whose methods match `*client.Client` signatures for writes, reads, deletes, imports, exports, schema operations and checks; its errors have the real client's codes and `spicedb: <op>: <server message>` shape. `client.ResolveOptions[O]` resolves any operation's options into an inspectable `ResolvedOptions` (with operation defaults), which the store uses to honor cursors, limits, export filters and `WithResponseMetadata` targets.
  - `Write` sends empty transactions (SpiceDB checks their preconditions and returns the current revision). Watch rejects object types with filters because SpiceDB does. Schema reads require an explicit consistency like every other read; `consistency.MinLatency()` matches the raw API's default.
  - `BulkCheckError` and `DeleteProgressError` implement `GRPCStatus` with the inner status, so `status.Convert` reports the server message once.
  - `protoconv.CheckBulkToProto` rebuilds a whole bulk-check response (per-item errors as statuses); `LookupSubjectToProto` fills the deprecated fields SpiceDB still sends.

- **Keep the library's requirements minimal.**
  - Magefiles live in `magefiles/` with their own `go.mod`, so consumers never inherit mage or the repository root module. gRPC, protobuf, genproto and gateway requirements stay at the lowest versions that work (currently authzed-go v1.11.0's); Dependabot version bumps are disabled for this module.

- **Keep documentation and generated clients executable.**
  - Generated facades forward native consistency, tokens, options, results and cancellation. Regenerated fixtures include positive execution and negative type checks.
  - Unit, build, lint and real transport tests cover option mapping, opaque token round trips, TLS, remote plaintext guards, stream release, upload cancellation, strict bulk responses, budgets and deletion progress. Examples assert the behavior they demonstrate and the runner reconciles example names.

- **Keep conditional lookup results distinct from unconditional grants.**
  - LookupResources preserves all permissionships by default, including conditional results. The optional `LookupPermissionshipFilter` selects only exact unconditional grants with `LookupHasPermissionOnly`.
  - Filtering happens client-side before yielding. Limits count yielded matches; skipped rows still advance cursors and page counts.
  - Conditional and unknown permissionships must never enter unconditional resource ID lists.

- **Preserve native relationship write results.**
  - Native `Write` and generated `Touch`, `Create`, and `Delete` return `client.WriteResult`, with its opaque revision in `Revision`. Generated facades forward the wrapper intact on every path.

- **Expose native relationship filters for watch streams.**
  - `UpdatesRequest.Filters` sends each native relationship filter to the server with OR semantics, including resource IDs, relations, and subject filters. Empty filters watch all relationships.
  - Object types and relationship filters are mutually exclusive and rejected locally when combined. Filter values are captured before streaming and preserve existing cancellation and resume behavior.

- **Keep generated clients at native capability parity.**
  - Generated `TypedClient` embeds `*client.Client`, exposing every native method directly with identical request, option, result, stream, and lifecycle semantics. `Client` remains available for interoperability; no native capability is excluded.
  - Schema-aware free functions and relationship helpers keep their existing compile-time constraints and forward native options. A reflection contract detects missing or changed promoted capabilities.
  - Typed transactions (`Txn`) defer the first error to `Build`/`WriteTxn`; `WriteTxn` never shadows native `Write`. Typed relationships round-trip with `rel.Relationship`. `RelationshipKey` identifies a tuple without caveat or expiration. Generated name collisions fail generation with a message naming both sources.
