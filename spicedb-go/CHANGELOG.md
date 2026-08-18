# Changelog

## Unreleased

### Breaking Changes

- **2026-08-17**: `Check`, `CheckOne`, and `CheckIter` now return a `CheckResult` (or `iter.Seq2[CheckResult, error]`) instead of a bare `bool`/`iter.Seq2[bool, error]`, so a caveated relationship whose context wasn't supplied at check time is distinguishable from a real denial instead of being silently collapsed to `false`. `CheckPermissionResponse.checked_at` — populated by the server on every check but never previously exposed by this client — is now reachable via `CheckResult.CheckedAt`, so read-your-writes is possible through the public API instead of requiring a raw gRPC stub. New type in `client/check_types.go`: `CheckResult{Permissionship, MissingContext, CheckedAt}` with `HasPermission() bool`, true only for `PermissionshipHasPermission`. `Permissionship` gains a fourth value, `PermissionshipNoPermission`, appended after `PermissionshipConditionalPermission` (not inserted alongside `PermissionshipUnspecified`) so the two pre-existing constants keep their `iota` values. `CheckAny`/`CheckAll` are unchanged in shape (still `(bool, error)`) but now count only `HasPermission()` results as granted — a Conditional result does not count, matching the fail-closed behavior of the new `CheckResult.HasPermission()`.

  `LookupResource`/`LookupSubject` also gain a `LookedUpAt string` field (from each response's `looked_up_at`), for the same read-your-writes reason — identical for every item in a single lookup stream.

  Before:
  ```go
  allowed, err := c.CheckOne(ctx, cs, "view", r)
  if err != nil { log.Fatal(err) }
  if allowed { /* ... */ } // true for both HAS_PERMISSION and (bug) CONDITIONAL_PERMISSION on some clients

  results, err := c.Check(ctx, cs, "view", rs...)
  for _, ok := range results { /* ok is a bare bool */ }
  ```
  After:
  ```go
  result, err := c.CheckOne(ctx, cs, "view", r)
  if err != nil { log.Fatal(err) }
  if result.HasPermission() { /* only true for a full grant */ }
  if result.Permissionship == client.PermissionshipConditionalPermission {
      // NOT a grant — result.MissingContext lists what the server needed
      // (e.g. ["now"]) and didn't get.
  }
  // Thread result.CheckedAt into consistency.AtLeast(...) for a later read
  // to observe this check.

  results, err := c.Check(ctx, cs, "view", rs...)
  for _, r := range results { r.HasPermission() /* ... */ }
  ```

  Write-surface audit for this change: `Write`, `DeleteRelationships`, and `WriteSchema` already returned a revision — no gap there. `ImportRelationships` (bulk import) does not, but that's because `ImportBulkRelationshipsResponse` has no `ZedToken` field in the proto at all — nothing for the client to expose.

- **2026-08-16** (carried forward from the cross-client error-hierarchy work, which made no CHANGELOG entries of its own): four sentinel errors were added to `client/errors.go` for `errors.Is` matching: `client.ErrUnavailable`, `client.ErrCanceled`, `client.ErrDeadlineExceeded`, `client.ErrResourceExhausted`. These complete the set alongside the six already present (`ErrNotFound`, `ErrAlreadyExists`, `ErrInvalidArgument`, `ErrFailedPrecondition`, `ErrPermissionDenied`, `ErrUnauthenticated`) — `client.Error.Code`/`client.ErrorCode` already had full gRPC-code coverage, but `errors.Is` sentinel matching was previously missing for these four codes. Additive; no existing sentinel changed meaning.

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

- **2026-08-18**: `CheckAll`/`CheckAllWithContext` now return `false` for zero relationships instead of vacuously `true`. Root `DESIGN.md`'s "An aggregate over zero checks is not a grant" clause names the hazard: Go's bare `for` loop aggregate falls through to `return true, nil` once it runs out of results, so `CheckAll(cs, "edit", docs.map(toRel)...)` was silently granted whenever the derived relationship slice came up empty — a filter that matched nothing, an upstream returning `nil`, a defensive empty slice. The guard runs before the RPC, so an empty call never reaches the server (unchanged from before: `CheckWithContext` already short-circuited on zero relationships). `CheckAny`/`CheckAnyWithContext` are unchanged — already correctly `false` on empty. No API shape change.
- **2026-08-17**: A check-time caveat context that `structpb.NewStruct` cannot convert (e.g. an unsupported value type) now returns an error from `Check`, `CheckWithContext`, `CheckOne(WithContext)`, `CheckAny(WithContext)`, `CheckAll(WithContext)`, and `CheckIter(WithContext)`, instead of silently sending the request with no context field. Previously the conversion error was discarded and the caller got back a `CONDITIONAL_PERMISSION`/`NO_PERMISSION` result indistinguishable from "the server legitimately needed more context than you supplied" — now the two cases can't be confused. No API shape change.
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

- **2026-08-17**: `Check`, `CheckOne`, `CheckAny`, `CheckAll`, and `CheckIter` each gain a `*WithContext` counterpart (`CheckWithContext`, `CheckOneWithContext`, `CheckAnyWithContext`, `CheckAllWithContext`, `CheckIterWithContext`) for supplying caveat context on a check. This closes a real gap: `CheckResult.MissingContext` (added above) told a caller a check needed caveat context like `"now"`, but there was previously no parameter anywhere on the check surface to supply it, making the information non-actionable. Purely additive — every existing call site (`client.Check(ctx, cs, "view", r1, r2)`, `client.CheckOne(...)`, etc.) is completely unaffected; the non-context methods are unchanged in signature and now simply delegate to their `*WithContext` counterpart with a `nil` context.

  Each `*WithContext` method takes an extra `checkContext map[string]any` parameter, positioned right after `permission` and before the (still variadic) relationships, e.g. `CheckWithContext(ctx, cs, permission, checkContext, rs ...rel.Relationship)`. New field on `rel.Relationship`: `CheckContext map[string]any`, set via the new `rel.Relationship.WithCheckContext(map[string]any)` builder, for supplying context to just one relationship in a call — distinct from the existing `CaveatContext`/`WithCaveat`, which is stored with a relationship on write, not sent on check. The two merge key by key for each item — item keys win on conflict, call-level keys absent from the item are retained, never wholesale-replaced, so a per-item override can't silently drop a shared key the caveat still needs.

  ```go
  // Existing call sites: unaffected.
  results, err := c.Check(ctx, cs, "view", r1, r2, r3)
  allAllowed, err := c.CheckAll(ctx, cs, "view", r1, r2, r3)

  // New: call-level default context, applied to every relationship in the call.
  result, err := c.CheckOneWithContext(ctx, cs, "conditional_view",
      map[string]any{"now": time.Now().Unix()}, r)

  // New: per-item context overrides a call-level default for just that one
  // relationship (merged key by key, not replaced).
  results, err = c.CheckWithContext(ctx, cs, "view",
      map[string]any{"now": N, "region": "us"},
      r1,                                                  // gets {"now": N, "region": "us"}
      r2.WithCheckContext(map[string]any{"region": "eu"}), // gets {"now": N, "region": "eu"}
  )
  ```

  An earlier version of this change added `opts ...CheckOption` to the existing methods, which forced `Check`/`CheckAny`/`CheckAll`'s `rs ...rel.Relationship` to become `rs []rel.Relationship` (Go allows only one variadic parameter, and it must be last, so the relationships parameter had to give up that slot to make room for trailing options). That degraded the common call site for every caller, including the majority who never touch caveat context, to serve the minority who do — reverted in favor of the parallel `*WithContext` methods above, which keep every existing signature byte-for-byte unchanged. See `spicedb-go/DESIGN.md` ("Checks" / "Check-time caveat context") for the full rationale and the merge rule.

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
