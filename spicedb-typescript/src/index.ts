// Client
export { SpiceDBClient, createSpiceDBClient, type SpiceDBClientOptions } from "./client.js";

// Consistency
export { Consistency, full, minLatency, atLeast, atLeastOrFull, atLeastOrMinLatency, snapshot } from "./consistency.js";

// Types
export {
  type Relationship,
  type RelationshipFilterOptions,
  type LookupResourcesParams,
  type LookupSubjectsParams,
  type CheckRequest,
  type WatchChange,
  type WatchEvent,
  type WatchOptions,
  type ExpandPermissionTreeParams,
  type ReflectSchemaOptions,
  type ComputablePermissionsParams,
  type DependentRelationsParams,
  type RelationReference,
  type RelationshipCountResult,
  type TreeOperation,
  type ObjectRef,
  type SubjectRef,
  type IntermediateNode,
  type LeafNode,
  type PermissionTree,
  type SchemaCaveatParameter,
  type SchemaCaveat,
  type SchemaRelation,
  type SchemaPermission,
  type SchemaDefinition,
  type ReflectSchemaResult,
  type SchemaDiff,
  Transaction,
  relationship,
  relationshipFromTuple,
} from "./types.js";

// Errors
export {
  SpiceDBError,
  PermissionDeniedError,
  NotFoundError,
  AlreadyExistsError,
  InvalidArgumentError,
  CancelledError,
  FailedPreconditionError,
  UnavailableError,
} from "./errors.js";
