# Changelog

## Unreleased

### Breaking

- `SpiceDBClient.watch()` now yields `tuple[list[Update], str]` instead of
  `tuple[list[core_pb2.RelationshipUpdate], str]`. Added `Update` and
  `UpdateOperation` native types.
- `SpiceDBClient.expand_permission_tree()` now returns `tuple[PermissionTree, str]`
  instead of `tuple[core_pb2.PermissionRelationshipTree, str]`. Added native
  `PermissionTree`, `IntermediateNode`, `LeafNode`, `ObjectRef`, `SubjectRef`,
  and `TreeOperation` types (mirrors the shape in `spicedb-go`).
- `SpiceDBClient.reflect_schema()` now returns a native `ReflectSchemaResult`
  (`definitions: list[SchemaDefinition]`, `caveats: list[SchemaCaveat]`,
  `revision: str`) instead of the raw proto response. Also fixes a
  pre-existing bug where the request built an invalid filter message
  (`ExpSchemaFilter`, which does not exist) instead of
  `ReflectionSchemaFilter`, which made every `reflect_schema()` call raise
  `AttributeError`.
- `SpiceDBClient.diff_schema()` now returns `list[SchemaDiff]` instead of the
  raw proto response. Added native `ReflectSchemaResult`, `SchemaDefinition`,
  `SchemaRelation`, `SchemaPermission`, `SchemaCaveat`,
  `SchemaCaveatParameter`, and `SchemaDiff` types (mirrors the shape in
  `spicedb-go/client/schema.go`). Note: `computable_permissions()`/
  `dependent_relations()` (which use `spicedb-go`'s native
  `RelationReference` type) are not yet implemented in this client at all —
  out of scope for this change.

## 0.1.0 (2026-03-16)

Initial release of the idiomatic Python SpiceDB client.
