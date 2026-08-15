# Changelog

## Unreleased

### Breaking Changes

- **2026-08-14**: `LookupResources` and `LookupSubjects` now yield native result structs instead of bare ID strings, so callers no longer have to blindly trust an ID string — they can see whether a match is a full grant or conditional on caveat context, and (critically) which subjects are excluded from a wildcard `"*"` match. Dropping `excluded_subjects` was a real over-grant risk: a caller that saw `"*"` and nothing else had no way to know some subjects were carved out of that grant. New types in `client/lookup_types.go`: `Permissionship`, `PartialCaveatInfo`, `LookupResource`, `ResolvedSubject`, `LookupSubject`.

  Before:
  ```go
  for resourceID, err := range c.LookupResources(ctx, cs, "document", "view", "user", "alice") {
      if err != nil { log.Fatal(err) }
      fmt.Println(resourceID)
  }

  for subjectID, err := range c.LookupSubjects(ctx, cs, "document", "doc1", "view", "user") {
      if err != nil { log.Fatal(err) }
      fmt.Println(subjectID) // "*" here silently meant "everyone", excluded subjects were dropped
  }
  ```
  After:
  ```go
  for resource, err := range c.LookupResources(ctx, cs, "document", "view", "user", "alice") {
      if err != nil { log.Fatal(err) }
      if resource.Permissionship != client.PermissionshipHasPermission {
          continue // conditional match; resource.PartialCaveat lists what's missing
      }
      fmt.Println(resource.ResourceID)
  }

  for subject, err := range c.LookupSubjects(ctx, cs, "document", "doc1", "view", "user") {
      if err != nil { log.Fatal(err) }
      if subject.Subject.SubjectID == "*" {
          excluded := map[string]bool{}
          for _, e := range subject.ExcludedSubjects {
              excluded[e.SubjectID] = true // MUST check before granting to "everyone"
          }
      }
      fmt.Println(subject.Subject.SubjectID)
  }
  ```

- **2026-08-14**: `ExpandResult.TreeRoot` (a leaked `*v1.PermissionRelationshipTree` proto type) is replaced with `ExpandResult.Tree`, a native `PermissionTree` (see `client/expand_tree.go`: `PermissionTree`, `IntermediateNode`, `LeafNode`, `ObjectRef`, `SubjectRef`, `TreeOperation`). No protobuf types are exposed from `ExpandPermissionTree` anymore.

  Before:
  ```go
  result, _ := c.ExpandPermissionTree(ctx, cs, "document", "1", "view")
  root := result.TreeRoot // *v1.PermissionRelationshipTree
  ```
  After:
  ```go
  result, _ := c.ExpandPermissionTree(ctx, cs, "document", "1", "view")
  tree := result.Tree // client.PermissionTree (native)
  ```

### Bug Fixes

- **2026-08-15**: `defaultDeletePageSize` (the default `DeleteRelationships` page size) is now 1,000, not 10,000. SpiceDB's default `--max-delete-relationships-limit` is 1,000, so a default (no-`DeleteOption`) `DeleteRelationships` call against a stock server previously failed with `provided limit 10000 is greater than maximum allowed of 1000`. No API shape change — only the default value sent when `WithDeleteLimit` isn't used.
- **2026-08-14**: `client.Error.Code` is now a native `client.ErrorCode` enum (`CodeUnknown`, `CodeNotFound`, `CodeAlreadyExists`, `CodeInvalidArgument`, `CodeFailedPrecondition`, `CodePermissionDenied`, `CodeUnauthenticated`, `CodeUnavailable`, `CodeResourceExhausted`, `CodeAborted`, `CodeDeadlineExceeded`, `CodeCanceled`, `CodeInternal`), replacing the raw `google.golang.org/grpc/codes.Code` that was previously exposed on the field. This closes a gap left by the earlier native-error-mapping fix, which mapped errors into `*client.Error` but left the raw gRPC code type on the struct. `errors.Is`/sentinel matching (`ErrNotFound`, etc.) is unchanged for callers; `errors.Unwrap` still exposes the underlying gRPC status error as an escape hatch. Any code that compared `err.(*client.Error).Code` against `codes.X` must switch to comparing against `client.CodeX`.

  Before:
  ```go
  if cerr, ok := err.(*client.Error); ok && cerr.Code == codes.NotFound {
      // handle not found
  }
  ```
  After:
  ```go
  if cerr, ok := err.(*client.Error); ok && cerr.Code == client.CodeNotFound {
      // handle not found
  }
  // or, unchanged: errors.Is(err, client.ErrNotFound)
  ```

### Features

- **2026-08-15**: `DeleteRelationships` now accepts variadic `DeleteOption`s, reaching the proto's `optional_preconditions` and `optional_limit` fields that were previously unset by the client. Additive — existing `c.DeleteRelationships(ctx, filter)` calls are unaffected (no preconditions, 1,000-item page size, partial deletions allowed, same as before). New: `client.WithDeleteMustMatch(filter)`/`client.WithDeleteMustNotMatch(filter)` add MUST_MATCH/MUST_NOT_MATCH preconditions (built the same way as `rel.Txn.MustMatch`/`MustNotMatch`) that guard the delete, rejecting it if unsatisfied; `client.WithDeleteLimit(n)` overrides the default 1,000-per-call page size. See `spicedb-go/DESIGN.md` ("Deletions") for the semantics of combining preconditions with auto-paging. New example: `examples/delete_relationships/`.

  ```go
  // Before (still works, unchanged):
  revision, err := client.DeleteRelationships(ctx, filter)

  // After (new, optional):
  revision, err := client.DeleteRelationships(ctx, filter,
      client.WithDeleteMustMatch(ownerGuard),
      client.WithDeleteLimit(1000),
  )
  ```
- **2026-08-14**: Added automatic retry with exponential backoff for transient gRPC errors (`UNAVAILABLE`, `RESOURCE_EXHAUSTED`, `ABORTED`), configured via gRPC's built-in service-config `retryPolicy` in `NewClient`'s dial options (proto-client tier). Up to 3 retries (4 total attempts), 100ms initial backoff, 2x multiplier, 5s max backoff. No public API change; callers can still override via `WithDialOptions`.
- **2026-08-14**: RPC and stream errors are now mapped to native `*client.Error` values inspectable via `errors.Is`/`errors.As`, instead of raw `%w`-wrapped gRPC status errors. New sentinels: `client.ErrNotFound`, `client.ErrAlreadyExists`, `client.ErrInvalidArgument`, `client.ErrFailedPrecondition`, `client.ErrPermissionDenied`, `client.ErrUnauthenticated`. Applies to every RPC call and every `iter.Seq2` streaming iterator (`ReadRelationships`, `LookupResources`, `LookupSubjects`, `ExportRelationships`, `Updates`), so mid-stream errors are native too. `errors.Unwrap` still exposes the underlying gRPC status error for advanced inspection.
- **2026-08-14**: Per-item errors from `Check`/`CheckOne`/`CheckAny`/`CheckAll`/`CheckIter` (surfaced via `BulkCheckPermissions` response pairs) are now mapped to native `*client.Error` values through the same `mapGRPCError` path as top-level RPC errors, instead of being string-formatted. `errors.Is(err, client.ErrInvalidArgument)` (and the other sentinels) now works for per-item bulk-check failures, not just top-level RPC failures.

## 0.1.0 (2026-03-16)

Initial release of the idiomatic Go SpiceDB client.

### Features

- **2026-03-16**: Initial implementation of the idiomatic Go client.
  - `consistency` package: `Full()`, `MinLatency()`, `AtLeast()`, `Snapshot()` strategy constructors
  - `rel` package: `Relationship` struct, `Interface` trait, `FromTriple`/`MustFromTriple`/`FromTuple`/`FromObjects` constructors, `WithCaveat`/`WithExpiration` modifiers, `Filter` builder, `Txn` transaction builder with `Create`/`Touch`/`Delete`/`MustNotMatch`/`MustMatch`, `Update` type for watch events
  - `client` package: `NewPlaintext`/`NewSystemTLS`/`NewWithOpts` constructors, `Check`/`CheckOne`/`CheckAny`/`CheckAll`/`CheckIter` (all via BulkCheckPermissions), `Write`/`ReadRelationships`/`DeleteRelationships`, `LookupResources`/`LookupSubjects`, `ReadSchema`/`WriteSchema`, `Updates` (watch)
  - Examples: `check_permission`, `write_relationships`, `read_relationships`, `lookup_resources`, `lookup_subjects`, `watch_changes`, `schema_management`, `bulk_operations`
- **2026-03-16**: Added missing API methods for full non-deprecated coverage.
  - `client` package: `ReflectSchema`, `ComputablePermissions`, `DependentRelations`, `DiffSchema` (schema reflection), `ExpandPermissionTree`, `ImportRelationships`, `ExportRelationships` (bulk import/export), `RegisterRelationshipCounter`, `CountRelationships`, `UnregisterRelationshipCounter` (experimental counters)
  - New types: `SchemaDefinition`, `SchemaRelation`, `SchemaPermission`, `SchemaCaveat`, `SchemaCaveatParameter`, `ReflectSchemaResult`, `RelationReference`, `SchemaDiff`, `ExpandResult`, `CountResult`
  - Examples: `schema_reflection`, `relationship_counters`
- **2026-03-16**: Added transparent cursor-based pagination, batching, and sentinel errors.
  - `ReadRelationships`, `LookupResources`, `ExportRelationships` now auto-paginate with internal cursors (512-item pages); `LookupSubjects` uses a single streaming call (no cursor support in SpiceDB yet)
  - `DeleteRelationships` auto-pages in 10,000-item batches until all matching rels deleted
  - `CheckIter` now batches input relationships in chunks of 1,000 (instead of collecting all first)
  - `rel` package: added sentinel errors `ErrInvalidResource`, `ErrInvalidRelation`, `ErrInvalidSubject`
