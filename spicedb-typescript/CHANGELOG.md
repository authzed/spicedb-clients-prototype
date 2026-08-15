# Changelog

## Unreleased

### Breaking Changes

- **2026-08-14**: `Consistency` is now an opaque native class instead of a
  re-exported protobuf-es type. `full()`, `minLatency()`, `atLeast()`,
  `snapshot()`, `atLeastOrFull()`, and `atLeastOrMinLatency()` now return the
  native `Consistency` class; the underlying proto message is no longer part
  of the public API. All `SpiceDBClient` methods that accept `consistency`
  unwrap it internally via an `@internal` `_toProto()` method before building
  the proto request. No call-site changes are required — construct consistency
  values only via the exported helper functions, never directly.

## 0.1.0 (2026-03-16)

Initial release of the idiomatic TypeScript SpiceDB client.

### Features

- **2026-03-16**: Initial implementation of the idiomatic TypeScript client.
  Created `src/client.ts`, `src/types.ts`, `src/consistency.ts`, `src/errors.ts`,
  `src/index.ts`. Full API coverage for all non-deprecated proto APIs:
  PermissionsService (checkPermission, checkPermissions, checkAny, checkAll,
  readRelationships, write, deleteRelationships, lookupResources,
  lookupSubjects, expandPermissionTree, importBulkRelationships,
  exportBulkRelationships), SchemaService (readSchema, writeSchema,
  reflectSchema, computablePermissions, dependentRelations, diffSchema),
  WatchService (watch), and ExperimentalService relationship counters
  (experimentalRegisterRelationshipCounter, experimentalCountRelationships,
  experimentalUnregisterRelationshipCounter). Added 8 examples covering all
  major use cases. Added experimental naming convention to DESIGN.md.
