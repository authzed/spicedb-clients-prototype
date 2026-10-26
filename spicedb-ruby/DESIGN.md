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
  - Unary operations share a 30-second default budget across attempts, backoff, chunks and pages. Streams accept explicit total budgets and otherwise remain unbounded.
  - Read retries are limited to safe statuses; mutations never replay. Streams never restart after exposing an item.
  - Closeable `Stream` values and block consumption release the underlying RPC, including blocked reads. Imports consume incrementally and cancel on producer failure.
- **Keep filters and deletion exact.**
  - Subject relation filters distinguish Any, Direct, and Named. Exact relationship filters preserve tuple identity.
  - Bounded deletion requires a positive total limit; delete-all uses a separate positive page size. Unknown completion states raise errors.
  - Interrupted deletion retains cumulative acknowledged counts, latest token, and cursor, including malformed final responses. Multiple pages are not atomic.
- **Cover the protocol with native results.**
  - Schema inspection, reflection, diffs, expansion, watch, import/export, counters, and experimental materialize APIs use native values.
  - Watch preserves transaction and revision metadata. Materialize streams preserve variants, changes, permission sets, cursor state and revisions; download results expose file metadata without fetching files.
  - Typed errors retain original causes and structured server details. `proto_client` exposes the same authenticated connection as a secondary escape hatch with shared ownership.
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
