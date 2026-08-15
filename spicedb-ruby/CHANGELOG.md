# Changelog

## Unreleased

### Changed

- **Breaking**: `expand_permission_tree` no longer leaks the raw protobuf `PermissionRelationshipTree` through `ExpandResult`. `ExpandResult#tree_root` is replaced by `ExpandResult#tree`, a native `SpiceDB::PermissionTree` built from new `Data.define` value types (`ObjectRef`, `SubjectRef`, `IntermediateNode`, `LeafNode`, `PermissionTree`), mirroring the Go client's native expand tree.

### Fixed

- **Delete page size correction**: `DEFAULT_DELETE_PAGE_SIZE` is now 10,000 (matching DESIGN.md spec), not 1,000
- `updates` now maps errors raised mid-stream (e.g. a garbage-collected watch revision) to native `SpiceDB::*Error` types instead of leaking the raw `GRPC::BadStatus`. The watch stream is intentionally not retried on error — only mapped — since retrying a live server-stream mid-flight risks replaying updates.
- Per-item errors from `check_permission`/`check_permissions`/`check_any`/`check_all` (via `BulkCheckPermissions`) now raise the specific `SpiceDB::*Error` subclass (e.g. `SpiceDB::InvalidArgumentError`) instead of the generic base `SpiceDB::Error`.
- `SpiceDB.to_spicedb_error` no longer misreads `Google::Rpc::Status#details` (a repeated `Any` field, unrelated to the error message) as the exception message; it now falls back to `#message` whenever `#details` isn't a usable string, fixing the message text for the per-item `BulkCheckPermissions` fix above.

### Documentation

- The three `experimental_*` relationship counter methods are now marked `@note Experimental` in their YARD docs to make clear they may change without following the backwards-compatibility mandate.

## 0.1.0 (2026-03-18)

Initial release of the idiomatic Ruby SpiceDB client.

### Features

- **Security-obvious constructors**: `SpiceDB::Client.new_plaintext` / `SpiceDB::Client.new_system_tls` make TLS posture explicit; block form for auto-close
- **Full API coverage**: all non-deprecated SpiceDB APIs exposed through idiomatic Ruby types
  - Permission checks (`check_permission`, `check_permissions`, `check_any`, `check_all`) via BulkCheckPermissions
  - Relationship CRUD with `Transaction` builder and preconditions
  - Streaming reads via `Enumerator` with transparent cursor pagination (supports `lazy`)
  - `lookup_resources` and `lookup_subjects`
  - Schema read, write, reflect, diff, computable permissions, dependent relations
  - Bulk import/export
  - Watch for relationship changes
  - Experimental relationship counters
- **`Data.define` value types**: `Relationship` and `Filter` are frozen, immutable value objects with `from_triple`, `from_tuple`, `with_caveat`, `with_expiration`
- **Explicit consistency**: every read requires a strategy (`full`, `min_latency`, `at_least`, `snapshot`, `at_least_or_full`, `at_least_or_min_latency`)
- **Exception hierarchy**: `SpiceDB::Error` with `PermissionDeniedError`, `NotFoundError`, `AlreadyExistsError`, `InvalidArgumentError`
- **Automatic retry**: exponential backoff for transient gRPC errors
- **Synchronous API**: uses Ruby's gRPC gem directly, no async complexity
- **9 examples** covering all major API surfaces, doubling as RSpec integration tests
- **Requires Ruby 3.2+** (for `Data.define`)
