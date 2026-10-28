# Ruby client design

- **Use native, immutable domain values.**
  - The [root design](../DESIGN.md) defines shared semantics, RPC coverage, security, and verification.
  - Ruby 3.2+ exposes `ObjectRef`, `SubjectRef`, `Relationship`, `PermissionCheck`, and native result records. Nested collections are copied and frozen; `Transaction` is a mutable builder.
  - `ZedToken.new(string)` snapshots an opaque string and `to_s` preserves it losslessly. Every public revision input and result uses `ZedToken`, with `nil` representing absence. Cursors remain separate values.
  - Consistency is required on reads: `full`, `min_latency`, `at_least(token)`, or `snapshot(token)`. Named fallback helpers use `nil` to select their fallback.
- **Make behavioral choices discoverable and validated.**
  - `Tracing::DISABLED` and `Tracing::ENABLED` select singular and bulk diagnostics, defaulting to disabled. Full recursive native traces preserve absent values and unknown server states.
  - `watch(content: [WatchContent::RELATIONSHIPS])` accepts explicit relationship, checkpoint, and schema selectors. Invalid cases fail before transport calls.
  - `TransportSecurity::TLS` is the constructor default; `PLAINTEXT` explicitly selects plaintext. Named TLS factories support default trust, custom CAs, and mTLS.
  - `RemoteCredentials::LOOPBACK_ONLY` rejects remote plaintext credentials; `ALLOW_PLAINTEXT` explicitly permits them through `new_plaintext(remote_credentials: ...)` and the normal constructor. TLS material is rejected with plaintext.
- **Preserve authorization meaning and caller data.**
  - `CheckResult#has_permission?` grants only unconditional permission; Ruby object truthiness is not an authorization predicate. Unknown states remain distinct.
  - Bulk checks validate cardinality and echoed requests, chunk at 1,000, and retain each chunk's revision and each error's absolute input index.
  - Item context overrides call context by key. JSON types and null survive conversion; unrepresentable data raises a typed field-specific error.
- **Bound operations and release resources deterministically.**
  - Unary operations share a 30-second default budget (`default_timeout:`) across attempts, backoff, chunks and pages; `default_deadline: DefaultDeadline::DISABLED` removes the default while a per-call `timeout:` still applies. Streams accept explicit total budgets and otherwise remain unbounded.
  - By default, idempotent reads retry UNAVAILABLE and ABORTED up to three times with full-jitter backoff from 100 ms. `retry_policy: RetryPolicy.new(max_retries:, retryable_codes:, initial_backoff:, max_backoff:, on_retry:)` replaces it client-wide (a caller may opt into RESOURCE_EXHAUSTED; `on_retry` receives a `RetryEvent`), `retries: Retries::DISABLED` turns retries off, and `stream_retries: StreamRetries::DISABLED` stops stream re-establishment (watch never re-establishes). Mutations never replay under any policy; streams never restart after exposing an item.
  - Closeable `Stream` values and block consumption release the underlying RPC, including blocked reads. Imports consume incrementally and cancel on producer failure; `batch_size:` sets relationships per message, one call is one stream and one transaction, and an aborted stream raises the server's final status (for example `AlreadyExistsError`), never a transport send failure.
- **Keep filters and deletion exact.**
  - Subject relation filters distinguish Any, Direct, and Named. Exact relationship filters preserve tuple identity.
  - `delete_relationships` sends one request. In the default `partial: DeletePartial::ALLOWED` mode it requires a positive `limit:`; `DeletePartial::DISALLOWED` is all-or-nothing (the server rejects a delete exceeding its limit) and `limit:` may be omitted for the server default. Delete-all uses a separate positive page size and reports each acknowledged batch's cumulative `DeleteResult` to `on_progress:`. Unknown completion states raise errors.
  - Interrupted deletion retains cumulative acknowledged counts, latest token, and cursor, including malformed final responses. Multiple pages are not atomic.
- **Cover the protocol with native results.**
  - Schema inspection, reflection, diffs, expansion, watch, import/export, counters, and experimental materialize APIs use native values.
  - Watch preserves transaction and revision metadata. Materialize streams preserve variants, changes, permission sets, cursor state and revisions; download results expose file metadata without fetching files.
  - Typed errors retain original causes and structured server details (see "Interoperate with grpc-ruby errors"). `proto_client` exposes the same authenticated connection as a secondary escape hatch with shared ownership.
- **Verify observable behavior.**
  - Tests cover immutable values, option validation, token propagation, strict bulk echoes, cumulative deletion, real TLS handshakes, cancellation, and deadlines.
  - Maintained examples assert behavior and use named choices; `bundle exec rspec` and `bundle exec rubocop --cache false` validate changes.

- **Make resource lookup grants explicit.**
  - `LookupPermissionshipFilter::ALL` is the default, preserving conditional results and missing context.
  - `LookupPermissionshipFilter::HAS_PERMISSION_ONLY` filters client-side to exact unconditional grants before yielding; unknown permissionship values never count as grants.
  - Limits count yielded matches. Skipped results advance cursors and further pages are fetched as needed.

- **Return explicit relationship write results.**
  - Transaction writes return immutable `WriteResult` values; access `result.revision` for the native `ZedToken` used by consistency and watch inputs.
  - Serialization belongs to the revision token. The result itself is not accepted as a token.

- **Name non-atomic deletion explicitly.**
  - `delete_all_relationships_non_atomic` deletes matching relationships across individually transactional batches. The complete operation is non-atomic; completed batches remain deleted if a later batch fails.
  - Each nonempty response cursor is sent on the following request. Empty or absent cursors continue without a cursor.
  - `DeleteResult.revision` identifies the acknowledged revision. Failures expose acknowledged counts and the latest revision through `delete_progress`.

- **Watch through native relationship filters.**
  - Watch accepts full `Filter` values, including resource IDs or prefixes, relations, and direct or named subject relations. Multiple filters are ORed; empty filters watch all relationship changes.
  - Requests snapshot filter collections before streaming. Nonempty `object_types` and `filters` are mutually exclusive and rejected before transport work.
  - `content:` including `WatchContent::SCHEMA` requests schema-change events, combinable with `WatchContent::CHECKPOINTS`; `WatchEvent#schema_updated` reports them separately from relationship updates.

- **Be one copy of the protocol bindings.**
  - `spicedb-proto` is the canonical Ruby binding for `authzed.api.*`, under the same require paths the official `authzed` gem uses. Its `bridge/authzed_bridge.rb` rewrites an `authzed` checkout to drop its generated files and depend on `spicedb-proto`; a second, unbridged copy raises `SpiceDBProto::BindingConflictError` at load time. See the proto tier's DESIGN.md, "Canonical binding", and MIGRATION.md.
  - Native support starts at SpiceDB 1.30. Older servers' trailer-only data is reachable through `call_options:` response metadata, not first-class API.

- **Interoperate with grpc-ruby errors.**
  - `SpiceDB::Error` subclasses `GRPC::BadStatus`, so existing `rescue GRPC::BadStatus => e` code catches native errors and `e.code` is the matching `GRPC::Core::StatusCodes` value: the server's code for server errors, the class's code for local errors (`InvalidArgumentError` is INVALID_ARGUMENT, an exhausted budget is DEADLINE_EXCEEDED) and for errors built by callers. Codes without a dedicated class keep their code on `SpiceDB::Error`. `to_status`/`to_rpc_status` work; `metadata` holds a server error's trailers.
  - `message` is the display message; `server_message` is the server's status message verbatim (nil for local errors); a mapped error keeps the original as `cause`.
  - `SpiceDB::Error.from_code(code, message)` builds the matching class for test doubles. `SpiceDB.code_of(err)` and `SpiceDB.error_class_of(err)` classify native errors, raw `GRPC::BadStatus` and `Google::Rpc::Status`. Native classes are not grpc's per-code classes, so `rescue GRPC::FailedPrecondition` does not catch `SpiceDB::FailedPreconditionError`; rescue the SpiceDB class or compare `code`.

- **Serve tooling without the raw escape hatch.**
  - `Client.from_channel(channel, interceptors: [], **options)` wraps a caller-owned `GRPC::Core::Channel`. The channel carries its own transport security; call credentials for a plaintext channel go in `interceptors` (for example `SpiceDBProto::BearerTokenInterceptor`). Operation options apply. `close` releases this client's streams and never closes the channel.
  - Every operation accepts `call_options: CallOptions.new(metadata:, credentials:, response_metadata:)`: request metadata and call credentials on every attempt, page and chunk, and a `ResponseMetadata` that captures the last request's headers and trailers (also on failure).
  - `SpiceDB::ProtoConv` exports native<->protocol conversions for objects, subjects, relationships, filters, preconditions, updates, consistency and tokens, check requests, and the results tools print (check results with recursive traces, lookups, expand trees, write results, read items, watch events).
  - `check_bulk(item_errors: BulkItemErrors::RETURNED)` puts each failed item's error, with its absolute `input_index`, on `CheckResult#error`; an errored item has `:unspecified` permissionship and never grants. `BulkItemErrors::RAISED` (default) fails the call.
  - Paged reads yield `ReadRelationship` (`relationship`, `cursor`, `read_at`) from `read_relationships`, `ExportedRelationship` (`relationship`, `cursor`; exports carry no revision) from `export_relationships`, and `LookupResource#cursor` from `lookup_resources`. Items delegate relationship readers (`resource_id`, `subject_id`, `to_s`, ...) and `Transaction` accepts them. `cursor:` resumes; resuming requires repeating the request and page size. Every page repeats the caller's consistency and one fixed `page_size:` (SpiceDB hashes both into the cursor, which pins the revision); a total `limit:` is enforced client-side.

- **Make native values observable and testable.**
  - `ObjectRef`, `SubjectRef`, `Relationship`, `Filter`, `SubjectRelationFilter`, `Update`, `Precondition` and `Transaction` have readable `to_s`; relationships, updates, filters, preconditions and transactions have `as_json`/`to_json` diagnostic forms (no parser). `Transaction#updates`/`#preconditions` return frozen `Update`/`Precondition` values; `Transaction#length`/`size` count updates without copying.
  - `Filter#matches?(relationship)` applies SpiceDB's filter semantics client-side, including Any/Direct/Named subject relations; caveats and expiration never affect matching.
  - `require 'spicedb/testing'` provides `SpiceDB::Testing::Store`, an in-memory double whose `write`, `read_relationships`, `delete_relationships`, `check` and `check_bulk` have the client's exact parameters and results: preconditions, CREATE conflicts and atomic transactions, filtered reads and deletes (including all-or-nothing), and results registered with `set_check_result`.
  - No panicking constructor variants: Ruby constructors already raise `InvalidArgumentError`, so `Relationship.from_tuple("...")` is the one-line constant/test form.
  - `Relationship#resource_type`, `#resource_id`, `#subject_type`, `#subject_id` and `#subject_relation` shorten the common paths; `with_resource`, `with_relation` and `with_subject` copy a relationship.
  - `Relationship.from_tuple`/`#to_s` cover SpiceDB's full syntax: `[caveat]`, `[caveat:{json}]` (keys sorted on output) and `[expiration:RFC3339]` (UTC, fractional seconds only when nonzero), and `...` as a direct subject. Specs use the vectors spicedb-gen/tuplecompat checks against SpiceDB's parser, and the `tooling_escape_hatches` example round-trips caveats and expiration through a live SpiceDB.

- **Document migration and dependencies.**
  - `MIGRATION.md` covers moving from the `authzed` gem; `AI_INTEGRATION_GUIDE.md` is the concrete guide for coding agents, with every snippet executed against a live SpiceDB. No typed generator exists for Ruby, so spicedb-gen rules do not apply.
  - The gemspecs declare the lowest working runtime versions (`grpc >= 1.73`, `google-protobuf >= 3.25`, `googleapis-common-protos-types >= 1.11`); rspec and rubocop are development dependencies, which consumers never install. Ruby has no mechanical API-compatibility gate; specs and review cover compatibility.
