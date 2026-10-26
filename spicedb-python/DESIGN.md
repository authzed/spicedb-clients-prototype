# Python client design

- **Expose native, immutable domain values.**
  - The [root design](../DESIGN.md) defines shared semantics, RPC coverage, security, and verification.
  - Frozen records snapshot nested caller collections. Relationships hold persisted caveats; PermissionCheck holds evaluation context.
  - `ZedToken(value: str)` is an opaque immutable revision value. `str(token)` preserves every character, including empty strings. All consistency, watch, schema, delete, counter and materialize revision inputs and results use it; cursors remain separate.
  - ConsistencyMode names FULL, MIN_LATENCY, AT_LEAST and SNAPSHOT; token modes require nonempty tokens. Named fallback helpers treat None and empty tokens as absent.
  - Consistency is explicit: full(), min_latency(), at_least(token), snapshot(token), and named optional-token fallbacks.
- **Make behavior discoverable through enums.**
  - Tracing.DISABLED/ENABLED and LookupDebug.DISABLED/ENABLED independently select check tracing and lookup diagnostics.
  - Wildcards.DEFAULT/INCLUDE/EXCLUDE selects lookup wildcard behavior.
  - WatchContent.RELATIONSHIPS/CHECKPOINTS/SCHEMA selects the requested watch content collection.
  - SubjectRelationMode.ANY/DIRECT/NAMED backs the any(), direct() and named(name) relation-filter factories.
  - Unknown option values raise InvalidArgumentError before sending requests. Binary result facts remain booleans.
- **Keep sync and asyncio APIs equivalent.**
  - spicedb.sync.SpiceDBClient and spicedb.aio.SpiceDBClient share argument names and native results. Async unary calls are awaited; stream methods return AsyncStream directly.
  - Streams support explicit close/aclose and context managers, cancelling the actual call even during blocked iteration. Clients release their streams on close.
  - Aio clients bind to one event loop. Imports consume sync or async producers incrementally and cancel on producer failure or deadline.
- **Require explicit transport choices.**
  - Constructors default to TransportSecurity.TLS; TransportSecurity.PLAINTEXT enables plaintext.
  - RemoteCredentials.REJECT is the default; RemoteCredentials.ALLOW explicitly permits credentials on non-loopback plaintext hostnames and IP addresses through ordinary constructors and generated connect factories.
  - Custom CA and paired client certificate/key PEM support TLS and mTLS. TLS material with plaintext is rejected. raw_grpc() preserves authentication and client ownership.
- **Preserve authorization semantics and diagnostics.**
  - check uses the singular RPC; check_bulk chunks at 1,000, validates every echoed request and cardinality, and preserves each chunk revision and per-item error index.
  - Only unconditional permission grants satisfy has_permission and bool(result). Unknown and conditional results never grant.
  - Optional recursive native traces preserve schema, caveat evaluation, resolution presence, timestamps, durations and expiration.
- **Bound operations and preserve progress.**
  - Unary operations default to one 30-second monotonic budget shared by retries, chunks and pages. Streams/import accept explicit total deadlines without a default lifetime.
  - Retry only idempotent reads on UNAVAILABLE/ABORTED, at most three retries with full-jitter exponential backoff; streams retry only before delivery.
  - Bounded deletion requires a positive limit. Delete-all uses a separate positive page size and attaches acknowledged progress to failures and cancellation.
  - Filters preserve Any, Direct and Named subject relations. JSON preserves null and rejects unsupported values with a field path.
- **Cover the protocol with native APIs.**
  - Schema reflection/diff, expansion, import/export, counters and materialize calls preserve native data and revisions. Experimental operations are named explicitly.
  - Materialize lookup preserves cursors and one bounded RPC; callers explicitly resume. Watch preserves every event variant and never reconnects automatically.
  - Lookup subjects bounds results locally with limit; page_size explicitly requests optional server pagination.
- **Verify behavior and typed facades.**
  - Unit tests exercise sync/aio parity, transport security, cancellation, budgets, bulk echoes, token round trips and option validation.
  - Generated facades forward enums, tokens, contexts and results; fixtures are regenerated and checked with positive and negative type cases.

- **Make resource lookup grants explicit.**
  - `LookupPermissionshipFilter.ALL` is the default, preserving conditional results and missing context.
  - `LookupPermissionshipFilter.HAS_PERMISSION_ONLY` filters client-side to exact unconditional grants before yielding; unknown permissionship values never count as grants.
  - Limits count yielded matches. Skipped results advance cursors and further pages are fetched as needed.

- **Return explicit relationship write results.**
  - Transaction writes return immutable `WriteResult` values; access `result.revision` for the native `ZedToken` used by consistency and watch inputs.
  - Generated sync and async `touch`, `create`, `delete`, and `write` methods preserve the same `WriteResult` contract.
  - Serialization belongs to the revision token. The result itself is not accepted as a token.

- **Name non-atomic deletion explicitly.**
  - `delete_all_relationships_non_atomic` deletes matching relationships across individually transactional batches. The complete operation is non-atomic; completed batches remain deleted if a later batch fails.
  - Each nonempty response cursor is sent on the following request. Empty or absent cursors continue without a cursor.
  - `DeleteResult.revision` identifies the acknowledged revision. Failures expose acknowledged counts and the latest revision through `delete_progress`.

- **Watch through native relationship filters.**
  - Watch accepts full `Filter` values, including resource IDs or prefixes, relations, and direct or named subject relations. Multiple filters are ORed; empty filters watch all relationship changes.
  - Requests snapshot filter collections before streaming. Nonempty `object_types` and `filters` are mutually exclusive and rejected before transport work.

- **Expose native capabilities directly on generated clients.**
  - Generated sync and async clients explicitly forward every native public operation, including bounded and non-atomic deletion, watch filters, schema operations, imports/exports, counters, and materialize APIs.
  - Schema-aware check and lookup overloads retain subject constraints while exposing all native behavioral options. Typed writes accept typed or native transactions and preserve metadata.
  - Streams are returned directly, preserving identity, cancellation and context-manager ownership; async unary calls await the native result.
