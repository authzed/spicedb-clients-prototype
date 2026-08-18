# Changelog

## Unreleased

### Added

- **Caveat context on the check surface**: `check_permission`/`check_permissions`/`check_any`/`check_all` gain an optional `context:` keyword, and `SpiceDB::Relationship` gains a `check_context` field (with a matching `with_check_context(context)` builder). Previously a `CheckResult` with `permissionship == :conditional_permission` told you `missing_context` — the caveat parameter names SpiceDB couldn't evaluate — but there was no way to actually supply them, leaving the caller stuck. `context:` is a call-level default fanned out onto every relationship in the call (all checks go through `BulkCheckPermissions`, whose wire format attaches context per item — `CheckBulkPermissionsRequestItem#context`, proto field 4 — since `CheckBulkPermissionsRequest` itself has no context field); `relationship.with_check_context({...})` overrides that default for one relationship only, merged **key-by-key** with the call-level context (the item's keys win on conflict, but call-level keys the item doesn't mention are retained — NOT a wholesale replacement, which would silently drop shared keys and land the caller right back in `CONDITIONAL_PERMISSION`). An item with no `check_context` inherits `context:` unchanged; if neither is supplied, no `context` field is set on the wire at all (`nil`, not an empty `Struct`). Purely additive: `context:` defaults to `nil` on every check method and `check_context` defaults to `nil` on `Relationship`, so no existing call site changes.

  `check_context` is check-time-only and a **different concept** from the pre-existing `Relationship#caveat_context` (write-time context embedded in `optional_caveat`, persisted to SpiceDB). `check_context` has no wire representation on the write path at all — it's read exclusively by the check methods — so it can never leak into a write and silently alter a stored relationship's caveat. `with_caveat` and `with_check_context` never touch each other's field.

  ```ruby
  # Call-level: applies to every relationship checked in this call.
  results = client.check_permissions(consistency, "view", rel1, rel2, context: { now: 42 })

  # Per-item: overrides the call-level default for just this relationship.
  rel = SpiceDB::Relationship.from_triple("doc", "1", "viewer", "user", "alice")
                              .with_check_context({ now: 42 })
  result = client.check_permission(consistency, "view", rel)
  result.has_permission? # => true, now that the caveat could be evaluated
  ```

- `delete_relationships` gains optional `must_match:`/`must_not_match:` preconditions and a `limit:` override, mirroring spicedb-go's `client.DeleteRelationships` + `WithDeleteMustMatch`/`WithDeleteMustNotMatch`/`WithDeleteLimit`. Previously the only way to guard a delete with a precondition was to route it through `write` with a `Transaction`; now `delete_relationships` can build and send `optional_preconditions` directly. As with the Go client, preconditions are a per-request proto field, so a delete spanning multiple auto-paged pages re-evaluates them on every page rather than checking once for the whole operation — pair a precondition with a `limit:` large enough to cover all matches in one call for all-or-nothing semantics. Additive keyword arguments; existing `delete_relationships(filter)` callers are unaffected.

  ```ruby
  # Only delete viewers if the document still has an owner.
  owner_guard = SpiceDB::Filter.new(resource_type: 'document').with_resource_id('doc1').with_relation('owner')
  viewer_filter = SpiceDB::Filter.new(resource_type: 'document').with_resource_id('doc1').with_relation('viewer')
  client.delete_relationships(viewer_filter, must_match: [owner_guard])

  # Override the default 1,000-per-call page size.
  client.delete_relationships(viewer_filter, limit: 500)
  ```

- `SpiceDB::LookupResource` and `SpiceDB::LookupSubject` gain a `looked_up_at` field — the ZedToken (`String`) the lookup was evaluated against, closing the same read-your-writes gap `CheckResult#checked_at` closes for checks (see below). Both types are constructed exclusively by `lookup_resources`/`lookup_subjects`, so this has no effect on normal call sites; it only matters if code constructs `LookupResource.new(...)`/`LookupSubject.new(...)` directly (e.g. in tests), which now requires the extra keyword.

### Changed

- **Breaking**: `check_permission`/`check_permissions` now return `SpiceDB::CheckResult`/`Array<SpiceDB::CheckResult>` instead of `Boolean`/`Array<Boolean>`. `CheckPermissionResponse#permissionship` is four-valued (`UNSPECIFIED`/`NO_PERMISSION`/`HAS_PERMISSION`/`CONDITIONAL_PERMISSION`) — a caveated relationship whose context wasn't supplied at check time comes back `CONDITIONAL_PERMISSION`, which the old Boolean return silently collapsed into either a grant or a denial, losing the "SpiceDB couldn't evaluate this" signal entirely. `CheckResult` carries `permissionship` (Symbol: `:unspecified`/`:no_permission`/`:has_permission`/`:conditional_permission` — the same native symbols `lookup_resources`/`lookup_subjects` use), `missing_context` (`Array<String>` of caveat parameter names that weren't supplied — `[]` unless conditional), `checked_at` (the ZedToken `String` the check was evaluated against, enabling read-your-writes chaining that was previously unreachable), and `has_permission?` (`true` ONLY for `:has_permission`). `check_any`/`check_all` are unaffected in shape — still plain `Boolean` — but now explicitly count ONLY `:has_permission`; a `:conditional_permission` result does NOT count as a grant for either (deliberately fail-closed).

  **Ruby has no way to override truthiness** — every object except `nil`/`false` is truthy, unlike Python's `__bool__` hook. `if result` on a `CheckResult` is unconditionally `true`, including for a conditional permission. Anyone migrating from the old Boolean API by writing `if client.check_permission(...)` gets a silent grant on an unevaluated caveat. **Callers MUST use `result.has_permission?` — never test the result itself.**

  Before:
  ```ruby
  allowed = client.check_permission(consistency, "view", rel)
  grant_access if allowed # Boolean — no permissionship signal
  ```
  After:
  ```ruby
  result = client.check_permission(consistency, "view", rel)
  grant_access if result.has_permission? # NOT `if result` — every CheckResult is truthy in Ruby
  ```

- **Breaking**: `lookup_resources`/`lookup_subjects` now yield native result objects instead of bare `String` IDs, closing an over-grant risk: the previous string-only shape silently dropped `excluded_subjects` for wildcard (`user:*`) matches, so a caller iterating IDs alone could treat a wildcard-excluded subject as granted. `lookup_resources` now yields `SpiceDB::LookupResource` (`resource_id`, `permissionship`, `partial_caveat`); `lookup_subjects` now yields `SpiceDB::LookupSubject` (`subject: ResolvedSubject`, `excluded_subjects: Array<ResolvedSubject>`). Both use the new `permissionship` Symbol (`:unspecified` / `:has_permission` / `:conditional_permission`) and `SpiceDB::PartialCaveatInfo`. Mirrors spicedb-go's `client/lookup_types.go`/`lookup.go`, including its fallback to the deprecated `subject_object_id`/`excluded_subject_ids` proto fields for servers that don't yet populate the modern `subject`/`excluded_subjects` fields.

  Before:
  ```ruby
  client.lookup_resources(consistency, "document", "view", "user", "alice").each do |resource_id|
    grant(resource_id) # String only — no permissionship signal
  end
  client.lookup_subjects(consistency, "document", "doc1", "view", "user").each do |subject_id|
    grant(subject_id) # wildcard "*" treated as unconditional — over-grant risk
  end
  ```
  After:
  ```ruby
  client.lookup_resources(consistency, "document", "view", "user", "alice").each do |result|
    next unless result.permissionship == :has_permission # skip conditional

    grant(result.resource_id)
  end
  client.lookup_subjects(consistency, "document", "doc1", "view", "user").each do |result|
    excluded = result.excluded_subjects.map(&:subject_id).to_set
    next if result.subject.subject_id == '*' && excluded.include?(caller_id)

    grant(result.subject.subject_id)
  end
  ```

- **Breaking**: `expand_permission_tree` no longer leaks the raw protobuf `PermissionRelationshipTree` through `ExpandResult`. `ExpandResult#tree_root` is replaced by `ExpandResult#tree`, a native `SpiceDB::PermissionTree` built from new `Data.define` value types (`ObjectRef`, `SubjectRef`, `IntermediateNode`, `LeafNode`, `PermissionTree`), mirroring the Go client's native expand tree.

  Before:
  ```ruby
  result = client.expand_permission_tree(consistency, "document", "1", "view")
  root = result.tree_root # proto PermissionRelationshipTree
  ```
  After:
  ```ruby
  result = client.expand_permission_tree(consistency, "document", "1", "view")
  tree = result.tree # SpiceDB::PermissionTree (native)
  ```

### Fixed

- **Delete page size correction**: `DEFAULT_DELETE_PAGE_SIZE` is now 1,000 (matching SpiceDB's default `--max-delete-relationships-limit`, so the default `delete_relationships` call works against a stock server), not 10,000 — the earlier "10,000" correction in this file was itself wrong
- `updates` now maps errors raised mid-stream (e.g. a garbage-collected watch revision) to native `SpiceDB::*Error` types instead of leaking the raw `GRPC::BadStatus`. The watch stream is intentionally not retried on error — only mapped — since retrying a live server-stream mid-flight risks replaying updates.
- Per-item errors from `check_permission`/`check_permissions`/`check_any`/`check_all` (via `BulkCheckPermissions`) now raise the specific `SpiceDB::*Error` subclass (e.g. `SpiceDB::InvalidArgumentError`) instead of the generic base `SpiceDB::Error`.
- `SpiceDB.to_spicedb_error` no longer misreads `Google::Rpc::Status#details` (a repeated `Any` field, unrelated to the error message) as the exception message; it now falls back to `#message` whenever `#details` isn't a usable string, fixing the message text for the per-item `BulkCheckPermissions` fix above.
- **`with_expiration` was non-functional in both directions.** The client wrote and
  read `optional_expiration`; the field on `authzed.api.v1.Relationship` is
  `optional_expires_at`. Writing an expiring relationship raised a bare
  `ArgumentError: Unknown field name 'optional_expiration'` (not translated into a
  `SpiceDB::Error`, since `with_retry` only rescues errors responding to `#code`), and
  reading one silently returned `nil` — so an expiring relationship read back as
  permanent. No TTL-based grant could be built. The read path's `respond_to?` guard,
  which is what made the failure silent, has been removed so a future rename fails
  loudly.
- **Reading back any caveated relationship raised `NoMethodError`.**
  `relationship_from_proto` called `transform_values` on `Struct#fields`, which is a
  `Google::Protobuf::Map` and not a `Hash`. Since that one conversion backs
  `read_relationships`, `export_relationships` and `updates`, no deployment using
  caveats could read, export or watch relationships at all. Caveat context is now read
  by dispatching on `google.protobuf.Value`'s `kind` oneof, so numbers, booleans,
  nulls, nested maps and lists are returned with their Ruby types intact — the previous
  `&:string_value` would have returned `""` for all of them had it run.

  This fixes the **read** path only. The **write** path still stringifies every caveat
  context value and is corrected separately, as part of the cross-client write-path work
  that also covers Go, C# and Java. So context written through this client's
  `with_caveat` currently reads back as strings — `with_caveat("x", { "n" => 42 })`
  reads back `"42"`, and that is the writer's bug, not the reader's. Context that
  reached the wire as a number (written by another client, by `zed`, or by this client
  once its writer is fixed) reads back as a `Float`, since `Value.number_value` is a
  `double` — `42` becomes `42.0`.

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
