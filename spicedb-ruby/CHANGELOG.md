# Changelog

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
