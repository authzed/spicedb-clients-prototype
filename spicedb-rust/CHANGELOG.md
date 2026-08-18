# Changelog

## Unreleased

### Fixes

- **`import_relationships` required a materialized `Vec<Relationship>`.** A caller streaming in a
  large import from an iterator/generator (a lazy computation, a DB cursor) was forced to collect
  it into a `Vec` first just to call this method -- unlike every other bulk/paginated RPC,
  `ImportBulkRelationships` is client-streaming, so the caller is the one producing an unbounded
  amount of data.
  - **Breaking:** `import_relationships`/`import_relationships_with_timeout` are now generic over
    `impl IntoIterator<Item = Relationship> + Send + 'static` (with `IntoIter: Send`) instead of
    a concrete `Vec<Relationship>` parameter. A `Vec` (or array) still works unchanged -- both
    implement `IntoIterator<Item = Relationship>` -- so this only breaks a caller that named the
    concrete parameter type explicitly. The background batching task now chunks the source
    iterator manually (`Iterator::take`/`by_ref`) instead of `Vec::chunks`, so only one batch
    (1,000 relationships) is ever held in memory at a time regardless of how the source is
    produced.
- **Watch resumability.** `updates` previously dropped `WatchResponse.changes_through`
  entirely and had no way to request `WATCH_KIND_INCLUDE_CHECKPOINTS` -- a prior audit's grep
  hit on `optional_update_kinds: Vec::new()` was a false positive: that's a required
  struct-literal field being zeroed, not a feature.
  - **Breaking:** `updates(&self, object_types, start_revision, include_checkpoints)` now
    returns `impl Stream<Item = Result<WatchEvent, SpiceDBError>>` instead of
    `impl Stream<Item = Result<Update, SpiceDBError>>`, gains a new `include_checkpoints: bool`
    parameter, and yields once per server response (a batch of updates) rather than flattening
    to one item per relationship update — a checkpoint response carries zero updates, so a
    per-update-only stream has no way to surface one at all.

    ```rust
    pub struct WatchEvent {
        pub updates: Vec<Update>,
        pub changes_through: String, // resume token; pass as start_revision to resume after a dropped stream
        pub is_checkpoint: bool,     // true for a checkpoint event, which carries no updates
    }
    ```
  - `WatchEvent::changes_through` is the proto's `changes_through` -- "This token can be used
    in a subsequent WatchRequest to resume watching from this point." Without it, a consumer
    whose stream dropped could only restart from its original `start_revision` (reprocessing
    everything since, possibly past the GC window) or from head (silently losing every change
    in the gap).
  - `include_checkpoints: true` requests `WATCH_KIND_INCLUDE_CHECKPOINTS` (plus
    `WATCH_KIND_INCLUDE_RELATIONSHIP_UPDATES`, since `optional_update_kinds` is
    empty-means-default and a non-empty list replaces rather than extends it) -- no prior way
    existed to ask for this at all. `WatchEvent::is_checkpoint` lets a caller tell "nothing
    changed, here is a fresh resume point" from "here are changes".
  - `examples/watch_changes.rs` updated for the new `WatchEvent` shape and to request
    checkpoints. `tests/support/mod.rs`'s `MockWatchService` gained a `requests()` accessor so
    tests can assert on the `WatchRequest` actually received, not just that the call
    succeeded. New tests in `tests/watch_test.rs`: a watch event exposes a usable resume
    token, `include_checkpoints` reaches the built `WatchRequest`, and a checkpoint event is
    distinguishable from one carrying updates. The three pre-existing behavioral tests updated
    for the new `WatchEvent` return type without weakening any existing assertion.
- **Call deadlines, per root `DESIGN.md` "RULE: A unary call must have a deadline".**
  Previously no method accepted a timeout and no client-level default existed, so a SpiceDB
  instance that accepted a connection but never answered hung every caller forever — the
  connection looks fine at the transport level, so no error is produced and there is nothing
  for retry logic to act on.
  - Every unary method gained a `_with_timeout(..., timeout: Duration)` sibling (e.g.
    `check_permission_with_timeout`, `write_with_timeout`, `read_schema_with_timeout`),
    mirroring the existing `_with_context` convention — fully additive, zero existing call
    sites changed. `delete_relationships_with` instead reads a new `DeleteOptions::with_timeout`
    field. Each `_with_timeout` variant sets `tonic::Request::set_timeout` on the request.
  - `SpiceDBClient::builder(...)` gained `.default_timeout(Duration)` (default 30s,
    `client::DEFAULT_TIMEOUT`), applied to any unary call that doesn't use a `_with_timeout`
    variant. 30s mirrors `authzed-node`'s `DEFAULT_DEADLINE_MS = 30_000` (its comment cites
    `grpc/grpc-node#541`). There is deliberately no way to construct a client whose unary calls
    have no bound at all.
  - Streaming methods (`read_relationships`, `lookup_resources`, `lookup_subjects`, `updates`,
    `export_relationships`) have no `_with_timeout` variant and are **not** bound by
    `default_timeout` — DESIGN.md's "Streaming calls MUST NOT inherit the unary default": these
    are long-lived by design (`updates` may legitimately run for the life of the process), and a
    30s cutoff would end a legitimate stream, which is a worse defect than the one this change
    fixes.
  - **Fix round 1 correction:** `import_relationships` (`ImportBulkRelationships`) also has an
    `import_relationships_with_timeout` sibling, but — unlike the unary methods above — it is
    client-streaming, not unary, and is now explicitly **excluded** from `default_timeout`: its
    duration scales with the size of the caller's dataset, not with server latency, so no fixed
    default is correct for it (root DESIGN.md, "RULE: A unary call must have a deadline",
    clause 3, amended to cover client-streaming and bidirectional RPCs, not only
    server-streaming). `import_relationships` is now unbounded;
    `import_relationships_with_timeout` still bounds the call explicitly. An earlier version of
    this fix incorrectly resolved `import_relationships` against `default_timeout`, which would
    have silently aborted large, legitimate multi-minute loads at 30 seconds.
  - **Behavioral finding:** tonic's own client-side timeout enforcement (its
    `transport::Channel`'s `GrpcTimeout` middleware, triggered by the `grpc-timeout` header
    `Request::set_timeout` sets) surfaces a purely local timeout as
    `Status::cancelled("Timeout expired")`, **not** `Status::deadline_exceeded` — confirmed
    against tonic 0.12.3's source (`TimeoutExpired`'s `Display` impl, matched via
    `Status::from_error`'s downcast handling). Left unmapped, that would make
    `SpiceDBError::DeadlineExceeded` (added earlier, but never actually producible) unreachable
    for exactly the case a deadline exists to guard against: a server that never responds at
    all. `error::from_grpc_status` now special-cases that exact `(code, message)` pair and maps
    it to `SpiceDBError::DeadlineExceeded` instead. `DEADLINE_EXCEEDED` was already excluded
    from `is_transient`, so a timeout is never auto-retried.
  - New `tests/deadline_test.rs`, against a real in-process gRPC server
    (`support::MockPermissionsService`, extended with `stall_check_bulk_permissions`/
    `stall_read_relationships`/`stall_import_bulk_relationships`) whose handlers deliberately
    stall: a unary call against a stub that never responds fails with
    `SpiceDBError::DeadlineExceeded` well before the server's stall completes (not a hang), a
    per-call `_with_timeout` overrides a much larger client default, a streaming call outlives a
    tiny unary default instead of inheriting it, and `import_relationships` both outlives the
    tiny unary default (proving the exclusion) and still fails promptly through
    `import_relationships_with_timeout` (proving the exclusion is from the default, not from the
    ability to bound the call). Every test is wrapped in `tokio::time::timeout` as a watchdog so
    a regression fails the suite instead of hanging CI.
  - **Fix round 1 correction (comment accuracy):** the remap comment in `error::from_grpc_status`
    previously claimed tonic gives no way to recover `TimeoutExpired`'s original type at the
    remap site. That's not quite right — `tonic::Status` implements `Error::source()` and
    `TimeoutExpired` is publicly exported, so a structural downcast is possible upstream of this
    function. What actually discards the type is this function's own `(code: i32, message:
    String)` signature. Corrected the comment; the string-match fix itself was already correct
    and unchanged.
  - New `examples/call_deadlines.rs`, run against a real SpiceDB rather than a mock: constructs a
    client via the documented `default_timeout` builder option, overrides it per-call, and
    confirms bulk import is unbounded by default.
- **Retry safety, per root `DESIGN.md` "RULE: Automatic retry is for idempotent operations
  only".** Three changes:
  - `RESOURCE_EXHAUSTED` is no longer retried. In SpiceDB it signals memory load-shed (retrying
    adds load to an already-overloaded server) or a deterministic `MaxDepthExceeded` (retrying can
    never succeed — it re-runs the most expensive class of check several times before surfacing
    the same error). Previously `error::is_transient` treated both `SpiceDBError::ResourceExhausted`
    and gRPC code 8 as transient.
  - Mutations (`write`, `delete_relationships`/`delete_relationships_with`, `write_schema`, the
    `experimental_register_relationship_counter`/`experimental_unregister_relationship_counter`
    calls) are no longer retried on a transient error, even though the underlying gRPC code is
    retryable. A `write()` carrying `OPERATION_CREATE` or preconditions is not idempotent: if it
    commits and the response is lost (a rolling restart, a proxy dropping the connection), a retry
    would surface `ALREADY_EXISTS`/`FAILED_PRECONDITION` for a write that in fact succeeded, and the
    caller would wrongly conclude it had failed. Reads still retry automatically. All five mutation
    call sites previously went through `SpiceDBClient::retry`; they now go through a new
    `SpiceDBClient::call_once`, which converts the error without retrying.
  - Backoff is now full-jitter (`jittered_backoff_ms`, `uniform(0, cap)`) instead of plain
    exponential doubling. Without jitter, every client in a fleet retries on the same schedule
    after a server restart, turning the recovery into a thundering herd. Requires a new direct
    dependency on `rand` (already present transitively).

  `src/error.rs`'s `test_is_transient_resource_exhausted` and
  `test_is_transient_resource_exhausted_via_from_grpc_status`, and their duplicates in
  `tests/error_test.rs`, previously asserted `is_transient(...)` was `true` for `RESOURCE_EXHAUSTED`;
  all four are inverted to assert `false`, since the old assertions were exactly the defect this
  fixes. New coverage in `tests/retry_safety_test.rs` (a mutation is attempted exactly once on a
  retryable error, a non-transient error, and `RESOURCE_EXHAUSTED`; a read is retried;
  `RESOURCE_EXHAUSTED` is never retried on a read) and a `jittered_backoff_ms` unit test in
  `src/client.rs` (backoff varies between calls). The mock `PermissionsService` harness in
  `tests/support/mod.rs` gained a real (previously `unimplemented!()`) `write_relationships` and
  failure-injection for both it and `check_bulk_permissions`.
- **`updates()` silently dropped every watch update whose operation it did not recognize.** The
  mapper's fallthrough arm was `_ => continue`, so an `OPERATION_UNSPECIFIED` — or any future
  operation value added to the proto after this client shipped — made the whole update vanish from
  the stream: a consumer mirroring the stream into a cache or index never learned the relationship
  had changed at all, with no error and no gap it could detect. That is worse than the wrong-write
  defect the other clients had, because there is nothing for the caller to inspect. `UpdateOperation`
  gains an `Unspecified` variant and the fallthrough arm now yields it, so the update is delivered
  and the caller can decide (re-read the relationship, or fail the mirror closed). Root `DESIGN.md`,
  "RULE: A conversion that cannot preserve meaning must fail", clause 2: server-supplied values the
  client does not recognise MUST NOT raise, and MUST map to the safe, non-permissive default —
  dropping is not that default. `Transaction`'s builder never produces `Unspecified`; if one is fed
  back into `write()` it is sent as `OPERATION_UNSPECIFIED` for the server to reject, rather than
  guessing at a mutation the caller never asked for.
- **`Filter::to_proto` built a `SubjectFilter` with `subject_type` defaulted to an empty string
  whenever only `subject_id`/`subject_relation` was set, instead of raising.** Unlike the other six
  clients, this was not silent: the condition included `|| self.subject_id.is_some()`, so
  `Filter::new("document").with_subject_id("alice")` still produced a `SubjectFilter` (with
  `subject_type: ""`), and the wire's `SubjectFilter.subject_type` is a required, pattern-validated
  field (`^([a-z][a-z0-9_]{1,61}[a-z0-9]/)*[a-z][a-z0-9_]{1,62}[a-z0-9]$`, which an empty string
  cannot match), so `delete_relationships`/`read_relationships`/etc. sent a request the server was
  guaranteed to reject with `InvalidArgument` — confirmed against a live SpiceDB instance: the
  server returns a `buf.validate` violation on `relationship_filter.optional_subject_filter.subject_type`
  and deletes nothing. That is better than the silent-wrong-answer defect in the other clients (no
  data was ever at risk), but still worse than a clear client-side error: every such call paid for
  a network round-trip only to fail with a raw server-side regex-validation message, rather than
  being rejected immediately with a message naming the problem. `to_proto` now returns
  `Err(SpiceDBError::InvalidArgument(..))` naming the field that was set without `subject_type`,
  matching the other clients' message shape, per root `DESIGN.md` "RULE: A conversion that cannot
  preserve meaning must fail", clause 1. `to_proto` is `pub(crate)`, so this ripples only within
  the crate: `preconditions_to_proto` (shared by `write` and `delete_relationships_with`),
  `delete_relationships_with`, `experimental_register_relationship_counter`, `read_relationships`,
  and `export_relationships` all now surface the conversion error before any RPC is attempted
  (the last two, being `Stream`-returning rather than `Result`-returning, surface it as the
  stream's first yielded `Err` instead of changing their signature). No pre-existing test asserted
  the old behavior, so none needed replacing.
- **`check_permissions`/`check_permissions_with_context` did not verify that
  the server returned as many pairs as were requested.** This closes the gap
  the malformed-pair fix below explicitly documented as still open: nothing
  checked that `inner.pairs` had as many entries as the `items` sent in the
  request, so a well-formed-but-short response (fewer pairs than items, no
  malformed entries among the pairs present) silently produced a
  `Vec<CheckResult>` shorter than `relationships` — every `results[i]` after
  the gap misaligned with `relationships[i]`, attributing one resource's
  answer to another. The proto guarantees pairs are returned in request
  order but says nothing about count. Each chunk's response is now checked
  with `inner.pairs.len() != items.len()` before pairs are mapped, returning
  `Err(error::internal(..))` (gRPC code 13, `SpiceDBError::Status { code:
  13, .. }`) naming both counts instead of silently returning a short
  result. This also makes the singular `check_permission`'s
  `results.remove(0)` unreachable for a zero-pair response — see the
  dedicated fix and regression test below.
- **`check_permission` could panic on the authorization hot path.** `Ok(results.remove(0))`
  panicked (`removal index (is 0) should be < len (is 0)`) inside the caller's task whenever the
  server returned zero pairs for a one-item request — this was the gap the length-guard fix above
  closes. `check_permission` now goes through `check_permissions_with_context`'s new
  `inner.pairs.len() != items.len()` guard before it ever reaches `results.remove(0)`: a
  zero-pair response is rejected with `Err(error::internal(..))` before `results` is returned, so
  the singular path can no longer observe an empty `Vec` there. Added
  `check_permission_errors_instead_of_panicking_on_zero_pairs` as a dedicated regression test
  proving the typed-error behavior directly on `check_permission` (not just the shared bulk path).
- **`check_all`/`check_all_with_context` returned `true` for zero
  relationships.** Rust's `Iterator::all` is vacuously `true` over an empty
  sequence. Root `DESIGN.md`'s "An aggregate over zero checks is not a
  grant" clause names the hazard: a gate like
  `check_all(cs, "edit", &docs_to_rels(&docs))` was silently granted
  whenever the derived relationships slice came up empty — a filter that
  matched nothing, an upstream returning an empty `Vec`. Both methods now
  guard the empty case before the aggregate and return `false` — neither
  reached the server for this case even before the fix, since
  `check_permissions_with_context` already short-circuits on an empty
  relationships slice. `check_any`/`check_any_with_context` are unchanged —
  already correctly `false` on empty (`Iterator::any`).
- **`new_system_tls` could not connect to any TLS server.** The client enabled tonic's
  `tls` feature but neither `tls-native-roots` nor `tls-webpki-roots`, and built its
  config with a bare `ClientTlsConfig::new()`. In tonic 0.12 that carries an empty
  trust-anchor set, so every handshake failed `UnknownIssuer` — against Authzed's
  managed service and against any self-hosted SpiceDB behind TLS alike. Because
  `connect()` is eager, it surfaced at construction as an opaque
  `SpiceDBError::Transport("transport error")`, which actively misdirected diagnosis.
  The client now enables `tls-native-roots` and calls `.with_native_roots()`, reading
  the OS trust store at runtime. (Each sibling client delegates to its own ecosystem's
  default trust source; those sources differ — see `DESIGN.md` — but none of them is
  empty.)

  The two tests that covered this asserted `is_err()` against an unreachable host, so
  they passed whether the failure was DNS or an empty trust store. They have been
  deleted and replaced with a test that completes a real handshake, per the new root
  `DESIGN.md` rule *"A system-TLS constructor must reach a real server."*


- **Fixed all 11 `examples/*.rs` hardcoding the preshared key
  `"somerandomkeyhere"`, which does not match `--grpc-preshared-key
  testtoken` in `docker-compose.test.yml`.** Running any example against
  this crate's own test harness failed with
  `PermissionDenied("invalid preshared key: invalid token")`. Examples now
  use `"testtoken"`, matching the harness. `docker-compose.test.yml` was
  left unchanged (the smaller fix, and changing it would have affected the
  `cargo test -- --ignored` tests that already pass against it).
- **Fixed `check_permissions`/`check_permissions_with_context` silently
  dropping an index from the results when a `CheckBulkPermissionsPair`
  arrived with its `response` oneof unset (neither `Item` nor `Error`).**
  The proto schema guarantees a well-behaved server never sends this, but
  nothing on the wire enforces it, and the old handling was:
  ```rust
  // Before:
  None => {}
  ```
  which silently skipped that index, so the returned `Vec<CheckResult>`
  came back shorter than the input `relationships` slice — every
  subsequent `results[i]` was then misaligned with `relationships[i]`, so
  a caller zipping results against inputs would attribute an answer to the
  wrong resource. A malformed pair now returns `Err(error::internal(..))`
  (gRPC code 13, `SpiceDBError::Status { code: 13, .. }`) instead of being
  silently skipped. Pre-existing; not introduced by the caveat-context work
  above.

  This closes the malformed-pair case specifically; it does not guarantee
  `results.len() == relationships.len()` in general. Nothing checks that
  `inner.pairs` itself has as many entries as the `items` sent in the
  request, so a server that returns a `pairs` list shorter than the batch
  (but with no malformed entries among the pairs it does return) still
  produces a `Vec<CheckResult>` shorter than `relationships`, silently and
  without error. Callers zipping results against inputs still rely on the
  server returning exactly one pair per item.
- **Fixed a per-item `CheckBulkPermissions` error being reported as a
  hardcoded `SpiceDBError::InvalidArgument` regardless of its actual gRPC
  status code.** `check_permissions` previously did:
  ```rust
  // Before:
  if let Some(Response::Error(err_resp)) = &pair.response {
      return Err(SpiceDBError::InvalidArgument(format!("check item {i}: {}", err_resp.message)));
  }
  ```
  so a per-item `PERMISSION_DENIED` was reported to the caller as
  `InvalidArgument` — worse than a generic fallback, since it actively
  misrepresents the failure mode. It now routes through the same
  `error::from_grpc_status` mapper every other RPC in this client uses:
  ```rust
  // After:
  Some(Response::Error(err_resp)) => {
      return Err(error::from_grpc_status(err_resp.code, format!("check item {i}: {}", err_resp.message)));
  }
  ```
  A per-item `PERMISSION_DENIED` now correctly surfaces as
  `SpiceDBError::PermissionDenied`.
- **`SpiceDBError` gained `DeadlineExceeded` and `ResourceExhausted` variants**
  (from a prior, unreleased change to this client's error hierarchy). Both
  gRPC codes previously fell through to the generic `Status { code, message }`
  fallback. As part of that change, `is_transient` was updated to recognize
  `SpiceDBError::ResourceExhausted` directly — before, `RESOURCE_EXHAUSTED`
  was only recognized via the `Status { code, .. }` match arm, which stopped
  matching once `from_grpc_status` started returning the dedicated
  `ResourceExhausted` variant for that code; without the fix, rate-limited
  calls would have silently stopped retrying.


- **Delete page size correction**: `DEFAULT_DELETE_PAGE_SIZE` is now 1,000 (matching SpiceDB's default `--max-delete-relationships-limit`, so the default `delete_relationships` call works against a stock server), not 10,000 — the earlier "10,000" correction in this file was itself wrong
- **Standardized the retryable gRPC code set to `{UNAVAILABLE, RESOURCE_EXHAUSTED,
  ABORTED}`**, aligning with the other idiomatic clients. `DEADLINE_EXCEEDED` is no
  longer treated as transient/retried — a deadline is a caller-set budget, and
  retrying past it silently extends an operation beyond the time the caller
  asked for. `ABORTED` (e.g. optimistic-concurrency/transaction conflicts) is
  now retried, since a retry is usually exactly the right response.
- **Lowered `MAX_RETRIES` from 5 to 3** (4 total attempts) and **capped the
  exponential backoff delay at 5s** per retry, so a long run of transient
  failures no longer produces unbounded per-attempt waits.
- **`updates()` (watch) now retries transient failures during stream
  *establishment***, not just during in-stream reads. Previously, if the
  initial `Watch` call failed with a transient error (e.g. `UNAVAILABLE`),
  the returned stream immediately yielded that error with no retry. It now
  retries establishment the same way other RPCs do. (Note:
  `import_relationships`, a client-streaming RPC, intentionally does not gain
  retry — retrying after a partial send would risk re-sending data.)

### Features

- **Caveat context on the check surface.** A prior change gave
  `CheckResult` a `missing_context: Vec<String>` field naming the caveat
  keys the server needed and didn't get — but there was no way to actually
  supply them. `Relationship` gains a `check_context: Option<HashMap<String,
  serde_json::Value>>` field (set via `.with_check_context(context)`) for
  per-item context, and each check method gains a `_with_context` sibling
  for a call-level default:

  ```rust
  use std::collections::HashMap;
  use spicedb::types::Relationship;

  let rel = Relationship::new("doc", "doc1", "view", "user", "alice", "")?;
  let mut context = HashMap::new();
  context.insert("now".to_string(), serde_json::json!(42));

  // Call-level default, applied to every relationship in the call:
  let result = client
      .check_permission_with_context(&cs, "view", &rel, Some(&context))
      .await?;

  // check_permissions_with_context / check_any_with_context /
  // check_all_with_context follow the same shape for the batch/aggregate
  // surface.
  ```

  Per-item context (via `Relationship::with_check_context`) merges with a
  call-level default **key by key, item wins**: the item's keys override
  matching call-level keys, and call-level keys the item doesn't specify are
  retained — not wholesale replacement, which would silently drop shared
  keys and reintroduce the exact "why is this still Conditional" confusion
  `missing_context` exists to resolve. When neither applies to an item, no
  `context` field is set on that item's wire request (`None`, not an empty
  `Struct`).

  `check_context` is a *different concept* from `caveat_context`:
  `caveat_context` is stored with a relationship as part of a **write** and
  supplies values for the caveat baked into that specific tuple;
  `check_context` is never sent on a write (`Relationship::to_proto` does
  not reference it) and instead supplies values for evaluating whatever
  caveat a permission **check** encounters. Keeping them on separate fields
  prevents a check-time value from silently leaking into a write and
  altering a stored relationship's caveat context.

  This is **purely additive** — `check_permission`, `check_permissions`,
  `check_any`, and `check_all` are unchanged and now delegate to their
  `_with_context` counterpart with `context: None`. No existing call site
  changes:

  ```rust
  // Before and after — identical:
  let result = client.check_permission(&cs, "view", &rel).await?;
  ```


- **`delete_relationships_with(filter, &DeleteOptions)`**: guarded deletes.
  Previously `delete_relationships` took only a filter, so the proto
  `optional_preconditions`/`optional_limit` fields were unreachable — there
  was no way to guard a delete on other relationship state, or to override the
  1,000-item auto-paging page size. `DeleteOptions` is a `Default`-derived
  builder:

  ```rust
  use spicedb::types::DeleteOptions;

  let options = DeleteOptions::new()
      .with_must_match(filter_that_must_exist)
      .with_must_not_match(filter_that_must_not_exist)
      .with_limit(1_000);
  let revision = client.delete_relationships_with(&filter, &options).await?;
  ```

  `delete_relationships(filter)` is unchanged and remains the ergonomic
  no-options path (it now delegates to `delete_relationships_with` with
  `DeleteOptions::default()`). As with write preconditions, delete
  preconditions are a per-request proto field: on a multi-page delete they are
  re-evaluated by the server on every page, so pair a precondition with
  `DeleteOptions::with_limit` set large enough to cover every matching
  relationship in one call for single-shot, all-or-nothing semantics.

### Breaking changes

- **`check_permission` and `check_permissions` now return `CheckResult`/`Vec<CheckResult>`
  instead of `bool`/`Vec<bool>`.** `CheckPermissionResponse.permissionship` is
  three-valued on the wire (`NO_PERMISSION`, `HAS_PERMISSION`,
  `CONDITIONAL_PERMISSION`), and the old bool return collapsed a
  `CONDITIONAL_PERMISSION` result — a caveated relationship whose context
  wasn't supplied at check time — into the same `false` as an outright
  denial. `CheckResult` keeps that distinction:

  ```rust
  // Before:
  // async fn check_permission(...) -> Result<CheckResult, SpiceDBError>; // CheckResult { has_permission: bool }
  // async fn check_permissions(...) -> Result<Vec<bool>, SpiceDBError>;

  // After:
  pub struct CheckResult {
      pub permissionship: Permissionship, // Unspecified | NoPermission | HasPermission | ConditionalPermission
      pub missing_context: Vec<String>,   // non-empty only when permissionship is ConditionalPermission
      pub checked_at: String,             // ZedToken; thread into consistency::at_least for read-your-writes
  }
  impl CheckResult {
      pub fn has_permission(&self) -> bool { /* true only for Permissionship::HasPermission */ }
  }

  // async fn check_permission(...) -> Result<CheckResult, SpiceDBError>;
  // async fn check_permissions(...) -> Result<Vec<CheckResult>, SpiceDBError>;

  let result = client.check_permission(&cs, "view", &rel).await?;
  if result.has_permission() {
      // granted outright
  }
  ```

  `check_any`/`check_all` keep their `bool` return, but now gate on
  `CheckResult::has_permission()` — a `ConditionalPermission` result does
  **not** count as granted for either (fail-closed by design: an unevaluated
  caveat must never silently widen a bulk any/all check into a grant).

  `Permissionship` gains a fourth value, `NoPermission`, inserted directly
  after `Unspecified` (this enum has no explicit discriminants, so there's no
  renumbering risk). It now serves both the check surface (`CheckResult`) and
  the lookup surface (`LookupResource`, `ResolvedSubject`); lookups never
  yield `NoPermission` — a resource/subject lacking the permission is simply
  absent from a lookup stream rather than yielded with that permissionship.

  `LookupResource` and `LookupSubject` gain a `looked_up_at: String` field
  (the revision the result was computed at — `CheckPermissionResponse.checked_at`'s
  read-your-writes counterpart for the lookup surface, previously
  unreachable through the public API).

  Rust's compile-time type checking makes the truthiness hazard other
  languages faced here (`if result:` silently granting on a truthy
  `CheckResult` object) structurally impossible — `if result` on a struct is
  a compile error, not a silent fail-open.


- **`lookup_resources` and `lookup_subjects` now yield native result structs
  instead of bare `String`s.** Previously, `lookup_subjects` silently dropped
  `excluded_subjects` — the list of subjects explicitly excluded from a
  wildcard (`"*"`) grant. A caller treating a wildcard-subject result as a
  blanket grant (the natural reading of a bare ID stream) could therefore
  grant access to a subject the server had explicitly excluded. Both methods
  now surface `permissionship` (full grant vs. conditional on caveat context)
  and, for `lookup_subjects`, `excluded_subjects`:

  ```rust
  // Before:
  // fn lookup_resources(...) -> impl Stream<Item = Result<String, SpiceDBError>>;
  // fn lookup_subjects(...) -> impl Stream<Item = Result<String, SpiceDBError>>;

  // After:
  // fn lookup_resources(...) -> impl Stream<Item = Result<LookupResource, SpiceDBError>>;
  // fn lookup_subjects(...) -> impl Stream<Item = Result<LookupSubject, SpiceDBError>>;

  use futures::StreamExt;

  let stream = client.lookup_resources(&consistency, "document", "view", "user", "alice");
  tokio::pin!(stream);
  while let Some(result) = stream.next().await {
      let result = result?;
      println!("{} ({:?})", result.resource_id, result.permissionship);
  }

  let stream = client.lookup_subjects(&consistency, "document", "doc1", "view", "user");
  tokio::pin!(stream);
  while let Some(result) = stream.next().await {
      let result = result?;
      if result.subject.subject_id == "*" {
          // MUST check excluded_subjects before treating "*" as a blanket grant.
          for excluded in &result.excluded_subjects {
              // excluded.subject_id does NOT have the permission.
          }
      }
  }
  ```

- **`updates()` (watch) is now a real `Stream`.** It changed from
  `async fn updates(...) -> Result<Vec<Update>, SpiceDBError>` to
  `fn updates(...) -> impl Stream<Item = Result<Update, SpiceDBError>>`. The old
  signature collected the entire server stream into a `Vec` and only returned
  once the stream ended — but watch is open-ended, so on a live watch it hung
  forever. Updates are now yielded incrementally as they occur. Consume the
  stream with `futures::StreamExt::next` (pin it first, e.g. `tokio::pin!`):

  ```rust
  use futures::StreamExt;
  let stream = client.updates(&object_types, None);
  tokio::pin!(stream);
  while let Some(update) = stream.next().await {
      let update = update?;
      // ...
  }
  ```

- **`read_relationships`, `lookup_resources`, `lookup_subjects`, and
  `export_relationships` are now real `Stream`s.** Each changed from an
  `async fn ... -> Result<Vec<T>, SpiceDBError>` that buffered the entire
  (auto-paginated) result set into a `Vec` before returning, to a
  `fn ... -> impl Stream<Item = Result<T, SpiceDBError>>` that yields items
  incrementally as they arrive. For the three that auto-paginate
  (`read_relationships`, `lookup_resources`, `export_relationships`), the next
  page is now fetched lazily — only once the current page has been fully
  drained by the caller — instead of the whole result set being pulled into
  memory up front. Consume with `futures::StreamExt::next` (pin it first,
  e.g. `tokio::pin!`):

  ```rust
  use futures::StreamExt;
  let stream = client.read_relationships(&consistency, &filter);
  tokio::pin!(stream);
  while let Some(rel) = stream.next().await {
      let rel = rel?;
      // ...
  }
  ```

- **`expand_permission_tree` now returns the full native `PermissionTree`,
  not just a revision.** Previously `ExpandResult` had only a `revision`
  field — the server's tree of intermediate (union/intersection/exclusion)
  and leaf (subject) nodes was discarded (a `// TODO: When spicedb-proto
  types are available` stub from the initial release). `ExpandResult` gained
  a `tree: PermissionTree` field; `PermissionTree` is a recursive, proto-free
  native type:

  ```rust
  // Before:
  // struct ExpandResult { revision: String }

  // After:
  // struct ExpandResult { tree: PermissionTree, revision: String }

  let result = client
      .expand_permission_tree(&consistency, "document", "doc1", "view")
      .await?;

  fn walk(tree: &spicedb::types::PermissionTree) {
      if let Some(leaf) = &tree.leaf {
          for subject in &leaf.subjects {
              println!("{}:{}", subject.subject_type, subject.subject_id);
          }
      }
      if let Some(intermediate) = &tree.intermediate {
          for child in &intermediate.children {
              walk(child);
          }
      }
  }
  walk(&result.tree);
  ```

## 0.1.0 (2026-03-18)

Initial release of the idiomatic Rust SpiceDB client.

### Features

- **Security-obvious constructors**: `SpiceDBClient::new_plaintext()` / `SpiceDBClient::new_system_tls()` make TLS posture explicit; builder pattern for advanced config
- **Full API coverage**: all non-deprecated SpiceDB APIs exposed through idiomatic Rust types
  - Permission checks (`check_permission`, `check_permissions`, `check_any`, `check_all`) via BulkCheckPermissions
  - Relationship CRUD with `Transaction` builder (borrows `&Relationship`, no moves)
  - Streaming reads via `impl Stream<Item = Result<T, SpiceDBError>>` with transparent cursor pagination
  - `lookup_resources` and `lookup_subjects`
  - Schema read, write, reflect, diff, computable permissions, dependent relations
  - Bulk import/export
  - Watch for relationship changes
  - Experimental relationship counters
- **Native Rust types**: `Relationship`, `Filter`, `Transaction` with `Debug`, `Clone`, `PartialEq`, `Eq` derives
- **Explicit consistency**: every read requires a `Strategy` (`full()`, `min_latency()`, `at_least()`, `snapshot()`, `at_least_or_full()`, `at_least_or_min_latency()`)
- **`thiserror` error enum**: `SpiceDBError` with `PermissionDenied`, `NotFound`, `AlreadyExists`, `InvalidArgument`, `Transport`, `Status` variants
- **`#[must_use]`** on check results to prevent silently ignoring permission checks
- **Automatic retry**: exponential backoff for transient gRPC errors
- **Async/await**: built on `tonic` + `tokio`
- **9 examples** covering all major API surfaces, runnable via `cargo run --example`
- **Rust Edition 2021**
