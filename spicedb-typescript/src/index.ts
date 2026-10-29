/**
 * The idiomatic TypeScript client for SpiceDB.
 *
 * Integrating SpiceDB into an application (including with an AI coding agent)?
 * Start with `AI_INTEGRATION_GUIDE.md` in this package: it shows when to use the
 * generated typed client (`spicedb-gen --lang typescript`) versus this native
 * client, and the safe patterns for checks, writes, reads, errors and tests.
 * Moving from `@authzed/authzed-node`? See `MIGRATION.md`.
 *
 * @packageDocumentation
 */
export { SpiceDBClient, createSpiceDBClient, type SpiceDBClientInterface, type SpiceDBClientOptions, type SpiceDBClientBehaviorOptions, type OperationName, type OperationRetryPolicies, type SpiceDBProtoClient, type TlsOptions } from "./client.js";
export { Consistency, full, minLatency, atLeast, snapshot, atLeastOrFull, atLeastOrMinLatency } from "./consistency.js";
export { OperationAbortError } from "./operation.js";
export { type JSONValue, type JSONObject, ZedToken, type ObjectRef, type SubjectRef, type Caveat, type Relationship, type CheckRequest, type SubjectRelationFilter, type SubjectFilter, type RelationshipFilter, type CallOptions, type CheckOptions, type Preconditions, type DeleteOptions, type DeleteAllOptions, type DeleteResult, type WriteResult, type ReadRelationshipsRequest, type LookupResourcesRequest, type LookupSubjectsRequest, type ExpandPermissionTreeRequest, type SchemaFilter, type ReflectSchemaRequest, type ComputablePermissionsRequest, type DependentRelationsRequest, type DiffSchemaRequest, type ExportRelationshipsRequest, type WatchRequest, type RoaringLookupResourcesRequest, type RoaringLookupResourcesResult, type WatchChange, type WatchEvent, type TreeOperation, type PermissionTreeNode, type PermissionTree, type ExpandResult, type SchemaCaveatParameter, type SchemaCaveat, type SchemaTypeReference, type SchemaRelation, type SchemaPermission, type SchemaDefinition, type ReflectSchemaResult, type RelationReference, type RelationsResult, type SchemaResult, type SchemaDiff, type SchemaDiffResult, type RelationshipCountResult, type Permissionship, type PartialCaveatInfo, type LookupResource, type ResolvedSubject, type LookupSubject, type CaveatEvaluation, type TraceResolution, type CheckDebugTrace, type DebugInformation, CheckResult, Transaction, relationship, relationshipFromTuple, relationshipToFilter, relationshipToString, objectRefToString, subjectRefToString, filterToString, filterMatches, updateToString, preconditionToString, type RelationshipUpdate, type Precondition, type ReadRelationship, type ExportedRelationship, type ImportOptions, type PermissionSetCursor, type PermissionSetReference, type PermissionSetMember, type PermissionSetChange, type WatchedPermission, type WatchPermissionSetsRequest, type PermissionSetEvent, type LookupPermissionSetsRequest, type LookupPermissionSet, type DownloadPermissionSetsRequest, type PermissionSetFile, type DownloadPermissionSetsResult, } from "./types.js";
export { SpiceDBError, PermissionDeniedError, NotFoundError, AlreadyExistsError, InvalidArgumentError, CancelledError, FailedPreconditionError, UnavailableError, DeadlineExceededError, ResourceExhaustedError, UnauthenticatedError, OutOfRangeError, errorCode, toConnectError, type SpiceDBErrorOptions } from "./errors.js";
export { Code } from "@connectrpc/connect";
export { DEFAULT_RETRY_POLICY, type RetryPolicy, type RetryEvent } from "./retry.js";
/** Native <-> protocol conversions for tools that print or persist protocol-shaped data. */
export * as protoconv from "./protoconv.js";
export { InMemorySpiceDB, FaultStage, SchemaRevision, type CheckRegistration, type CheckResolver, type InMemoryOperation, type InMemoryCallArguments, type RecordedCall, type Fault, type FaultHook } from "./testing.js";

export { SubjectRelationKind, Tracing, LookupDebug, LookupPermissionshipFilter, Wildcards, WatchContent, TransportSecurity, RemoteCredentials, Retries, StreamRetries, DefaultDeadline, BulkItemErrors, DeletePartial, Paging } from "./modes.js";
