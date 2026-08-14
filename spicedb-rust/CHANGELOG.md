# Changelog

## Unreleased

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
