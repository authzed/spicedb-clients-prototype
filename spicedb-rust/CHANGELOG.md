# Changelog

## Unreleased

### Fixes

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
