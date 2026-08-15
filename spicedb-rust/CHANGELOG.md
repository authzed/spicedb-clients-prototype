# Changelog

## Unreleased

### Fixes

- **Delete page size correction**: `DEFAULT_DELETE_PAGE_SIZE` is now 10,000 (matching DESIGN.md spec), not 1,000
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

### Breaking changes

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
