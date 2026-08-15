# Changelog

## Unreleased

### Added

- `SpiceDBClient.computable_permissions()` and `SpiceDBClient.dependent_relations()`
  — previously missing `SchemaService` RPCs (API-coverage gap). Both take
  `(consistency, definition_name, relation_name_or_permission_name)` and
  return `list[RelationReference]` (new native type: `definition_name`,
  `relation_name`, `is_permission`), mirroring `spicedb-go`'s
  `ComputablePermissions`/`DependentRelations`/`RelationReference`
  (`spicedb-go/client/schema.go`). These are stable `SchemaService` RPCs, not
  experimental.

  ```python
  permissions = await client.computable_permissions(full(), "document", "viewer")
  for p in permissions:  # p: RelationReference
      print(p.relation_name, p.is_permission)

  relations = await client.dependent_relations(full(), "document", "view")
  ```

### Fixed

- `Update._from_proto()` no longer raises a bare `KeyError` when the wire
  `RelationshipUpdate.operation` is `OPERATION_UNSPECIFIED` or any other
  unrecognized value — that killed a live `watch()` stream with a
  non-`SpiceDBError`. Unknown/unspecified operations now map to the new
  `UpdateOperation.UNSPECIFIED` enum member instead.
- `SpiceDBClient.check_permissions()` (and `check_permission`/`check_any`/
  `check_all`, which delegate to it) no longer fabricates a generic
  `SpiceDBError` (mapped from a synthetic `INTERNAL` gRPC error) when a
  `CheckBulkPermissions` response pair carries a per-item error. It now maps
  the real `google.rpc.Status` on the pair to the correct typed
  `SpiceDBError` subclass (e.g. `InvalidArgumentError`, `NotFoundError`) via
  the new `error_from_status_proto()` helper in `spicedb.errors`, preserving
  both the real status code and message.
- `Consistency` (the native type introduced for the `Consistency` proto
  removal, see below) is now exported from the package root — `from spicedb
  import Consistency` works for type annotations, matching the constructor
  functions which were already exported.

### Breaking

- `SpiceDBClient.lookup_resources()` and `SpiceDBClient.lookup_subjects()`
  now yield native `LookupResource`/`LookupSubject` result dataclasses
  instead of bare ID strings, so callers no longer have to blindly trust an
  ID string — they can see whether a match is a full grant or conditional on
  caveat context, and (critically) which subjects are excluded from a
  wildcard `"*"` match. Dropping `excluded_subjects` was a real over-grant
  risk: a caller that saw `"*"` and nothing else had no way to know some
  subjects were carved out of that grant. New types in `spicedb/types.py`:
  `Permissionship`, `PartialCaveatInfo`, `LookupResource`, `ResolvedSubject`,
  `LookupSubject` — mirrors `spicedb-go`'s reference design
  (`spicedb-go/client/lookup_types.go`).

  Before:
  ```python
  async for resource_id in client.lookup_resources("document", "view", ("user:alice", ""), full()):
      print(resource_id)

  async for subject_id in client.lookup_subjects(("document", "doc1"), "view", "user", full()):
      print(subject_id)  # "*" here silently meant "everyone", excluded subjects were dropped
  ```
  After:
  ```python
  async for resource in client.lookup_resources("document", "view", ("user:alice", ""), full()):
      if resource.permissionship != Permissionship.HAS_PERMISSION:
          continue  # conditional match; resource.partial_caveat lists what's missing
      print(resource.resource_id)

  async for subject in client.lookup_subjects(("document", "doc1"), "view", "user", full()):
      if subject.subject.subject_id == "*":
          excluded = {e.subject_id for e in subject.excluded_subjects}  # MUST check before granting to "everyone"
      print(subject.subject.subject_id)
  ```
- `SpiceDBClient.watch()` now yields `tuple[list[Update], str]` instead of
  `tuple[list[core_pb2.RelationshipUpdate], str]`. Added `Update` and
  `UpdateOperation` native types.

  Before:
  ```python
  async for updates, revision in client.watch():
      for u in updates:  # u: core_pb2.RelationshipUpdate (proto)
          print(u.operation, u.relationship)
  ```
  After:
  ```python
  async for updates, revision in client.watch():
      for u in updates:  # u: spicedb.Update (native)
          print(u.operation, u.relationship)  # operation: UpdateOperation
  ```
- `SpiceDBClient.expand_permission_tree()` now returns `tuple[PermissionTree, str]`
  instead of `tuple[core_pb2.PermissionRelationshipTree, str]`. Added native
  `PermissionTree`, `IntermediateNode`, `LeafNode`, `ObjectRef`, `SubjectRef`,
  and `TreeOperation` types (mirrors the shape in `spicedb-go`).

  Before:
  ```python
  tree, revision = await client.expand_permission_tree(("document", "1"), "view", full())
  # tree: core_pb2.PermissionRelationshipTree (proto)
  ```
  After:
  ```python
  tree, revision = await client.expand_permission_tree(("document", "1"), "view", full())
  # tree: spicedb.PermissionTree (native dataclass)
  for subject in tree.leaf.subjects:  # leaf: LeafNode | None
      print(subject.subject_id)
  ```
- `SpiceDBClient.reflect_schema()` now returns a native `ReflectSchemaResult`
  (`definitions: list[SchemaDefinition]`, `caveats: list[SchemaCaveat]`,
  `revision: str`) instead of the raw proto response. Also fixes a
  pre-existing bug where the request built an invalid filter message
  (`ExpSchemaFilter`, which does not exist) instead of
  `ReflectionSchemaFilter`, which made every `reflect_schema()` call raise
  `AttributeError`.

  Before:
  ```python
  resp = await client.reflect_schema(full())
  for d in resp.definitions:  # resp: proto ReflectSchemaResponse
      print(d.name)
  ```
  After:
  ```python
  result = await client.reflect_schema(full())
  for d in result.definitions:  # result: native ReflectSchemaResult
      print(d.name)  # d: SchemaDefinition
  ```
- `Consistency` is now an opaque native frozen dataclass instead of a
  `permission_service_pb2.Consistency` proto alias. `full()`, `min_latency()`,
  `at_least()`, `snapshot()`, `at_least_or_full()`, and
  `at_least_or_min_latency()` all still return `Consistency` and every
  `consistency=...` call site is unaffected — the proto is now hidden behind
  a private `_to_proto()` accessor used internally by `SpiceDBClient`. Code
  that reached into the proto's oneof fields directly (e.g.
  `full().fully_consistent`) will break; construct via the helper functions
  and let the client handle the rest.

  Before:
  ```python
  cs = full()
  if cs.fully_consistent:  # direct proto oneof field access
      ...
  ```
  After:
  ```python
  cs = full()
  # no direct field access; construct via full()/at_least()/etc. and pass
  # `cs` straight to client calls — internals handle the rest
  ```
- `SpiceDBClient.diff_schema()` now returns `list[SchemaDiff]` instead of the
  raw proto response. Added native `ReflectSchemaResult`, `SchemaDefinition`,
  `SchemaRelation`, `SchemaPermission`, `SchemaCaveat`,
  `SchemaCaveatParameter`, and `SchemaDiff` types (mirrors the shape in
  `spicedb-go/client/schema.go`).

  Before:
  ```python
  resp = await client.diff_schema(full(), new_schema)
  for d in resp.diffs:  # resp: proto DiffSchemaResponse
      print(d.definition_added.name)
  ```
  After:
  ```python
  diffs = await client.diff_schema(full(), new_schema)
  for d in diffs:  # diffs: list[SchemaDiff] (native)
      print(d.kind, d.definition_name)
  ```

## 0.1.0 (2026-03-16)

Initial release of the idiomatic Python SpiceDB client.
