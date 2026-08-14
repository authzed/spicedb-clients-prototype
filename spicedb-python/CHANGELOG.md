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

## 0.1.0 (2026-03-16)

Initial release of the idiomatic Python SpiceDB client.
