// Client
export { SpiceDBClient, createSpiceDBClient, type SpiceDBClientOptions } from "./client.js";

// Consistency
export { Consistency, full, minLatency, atLeast, atLeastOrFull, atLeastOrMinLatency, snapshot } from "./consistency.js";

// Types
export {
  type Relationship,
  type RelationshipFilterOptions,
  type DeleteOptions,
  type LookupResourcesParams,
  type LookupSubjectsParams,
  type CheckRequest,
  type CheckOptions,
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
  type Permissionship,
  type PartialCaveatInfo,
  type LookupResource,
  type ResolvedSubject,
  type LookupSubject,
  CheckResult,
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
  DeadlineExceededError,
  ResourceExhaustedError,
  UnauthenticatedError,
  OutOfRangeError,
  type SpiceDBErrorOptions,
} from "./errors.js";
