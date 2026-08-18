import { create, type JsonObject } from "@bufbuild/protobuf";
import {
  type Relationship as ProtoRelationship,
  RelationshipSchema,
  ObjectReferenceSchema,
  SubjectReferenceSchema,
  ContextualizedCaveatSchema,
  type RelationshipUpdate as ProtoRelationshipUpdate,
  RelationshipUpdateSchema,
  RelationshipUpdate_Operation,
  type RelationshipFilter as ProtoRelationshipFilter,
  RelationshipFilterSchema,
  SubjectFilterSchema,
  SubjectFilter_RelationFilterSchema,
  type Precondition as ProtoPrecondition,
  PreconditionSchema,
  Precondition_Operation,
  type DeleteRelationshipsRequest as ProtoDeleteRelationshipsRequest,
  DeleteRelationshipsRequestSchema,
  type PermissionRelationshipTree as ProtoPermissionRelationshipTree,
  AlgebraicSubjectSet_Operation,
  type ReflectionDefinition as ProtoReflectionDefinition,
  type ReflectionCaveat as ProtoReflectionCaveat,
  type ReflectionRelationReference as ProtoReflectionRelationReference,
  type ReflectionSchemaDiff as ProtoReflectionSchemaDiff,
  type PartialCaveatInfo as ProtoPartialCaveatInfo,
  type ResolvedSubject as ProtoResolvedSubject,
  type LookupResourcesResponse as ProtoLookupResourcesResponse,
  type LookupSubjectsResponse as ProtoLookupSubjectsResponse,
  LookupPermissionship,
  type CheckPermissionResponse as ProtoCheckPermissionResponse,
  type CheckBulkPermissionsResponseItem as ProtoCheckBulkPermissionsResponseItem,
  CheckPermissionResponse_Permissionship,
} from "@spicedb/proto";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";

/**
 * Represents a relationship between a resource and a subject.
 */
export interface Relationship {
  resourceType: string;
  resourceId: string;
  resourceRelation: string;
  subjectType: string;
  subjectId: string;
  subjectRelation?: string;
  caveatName?: string;
  caveatContext?: Record<string, unknown>;
  expiration?: Date;
}

/**
 * Filter for querying relationships.
 */
export interface RelationshipFilterOptions {
  resourceType: string;
  resourceId?: string;
  resourceIdPrefix?: string;
  resourceRelation?: string;
  subjectType?: string;
  subjectId?: string;
  subjectRelation?: string;
}

/**
 * Options for {@link SpiceDBClient.deleteRelationships}: preconditions that
 * guard the delete, and an optional limit on how many relationships a single
 * call deletes.
 *
 * Mirrors spicedb-go's `WithDeleteMustMatch`/`WithDeleteMustNotMatch`/
 * `WithDeleteLimit` (client/relationships.go) and spicedb-python's
 * `delete_relationships` keyword arguments.
 */
export interface DeleteOptions {
  /**
   * Preconditions requiring at least one relationship matching each filter
   * to exist at evaluation time. If any `mustMatch` filter has no matches,
   * the server rejects the delete and deletes nothing.
   */
  mustMatch?: RelationshipFilterOptions[];
  /**
   * Preconditions requiring that no relationship matches each filter at
   * evaluation time. If any `mustNotMatch` filter has a match, the server
   * rejects the delete and deletes nothing.
   */
  mustNotMatch?: RelationshipFilterOptions[];
  /**
   * Bounds how many relationships this call deletes. If more relationships
   * match the filter than `limit`, only `limit` of them are deleted by this
   * call (setting `limit` automatically allows this partial deletion on the
   * server, since the server otherwise rejects a limited delete found to
   * span more matches than the limit). This does not auto-page — call again
   * with the same filter to continue deleting what remains.
   */
  limit?: number;
}

/**
 * Parameters for looking up resources.
 */
export interface LookupResourcesParams {
  resourceType: string;
  permission: string;
  subjectType: string;
  subjectId: string;
  subjectRelation?: string;
  context?: Record<string, unknown>;
  limit?: number;
}

/**
 * Parameters for looking up subjects.
 */
export interface LookupSubjectsParams {
  resourceType: string;
  resourceId: string;
  permission: string;
  subjectType: string;
  subjectRelation?: string;
  context?: Record<string, unknown>;
  limit?: number;
}

/**
 * A check to perform on a resource/permission/subject combination.
 */
export interface CheckRequest {
  resourceType: string;
  resourceId: string;
  permission: string;
  subjectType: string;
  subjectId: string;
  subjectRelation?: string;
  context?: Record<string, unknown>;
}

/**
 * Call-level defaults for `checkPermission`, `checkPermissions`,
 * `checkAny`, and `checkAll` on `SpiceDBClient`.
 */
export interface CheckOptions {
  /**
   * Default caveat context applied to every check that this call evaluates.
   * Merged key-by-key with each individual check's own
   * {@link CheckRequest.context} — for any key present in both, the check's
   * own value wins; default keys the check does not override are retained.
   * This is a key-level merge, NOT a wholesale replacement: a check
   * supplying one key does not drop the other default keys. If neither this
   * default nor the check's own `context` is supplied, no context field is
   * set on the request (never an empty Struct).
   *
   * @example
   * ```typescript
   * // Both items get `now`; the second item's own `region` wins over the
   * // default for that key alone.
   * await client.checkPermissions(
   *   full(),
   *   [check1, { ...check2, context: { region: "eu" } }],
   *   { context: { now: 42, region: "us" } },
   * );
   * ```
   */
  context?: Record<string, unknown>;
}

/**
 * A change event from the Watch API.
 */
export interface WatchChange {
  operation: "create" | "touch" | "delete";
  relationship: Relationship;
}

/**
 * A watch event containing changes and metadata.
 */
export interface WatchEvent {
  changes: WatchChange[];
  revision: string;
  metadata?: Record<string, unknown>;
  schemaUpdated: boolean;
  isCheckpoint: boolean;
}

/**
 * Options for watching changes.
 */
export interface WatchOptions {
  objectTypes?: string[];
  startRevision?: string;
}

/**
 * Parameters for expanding a permission tree.
 */
export interface ExpandPermissionTreeParams {
  resourceType: string;
  resourceId: string;
  permission: string;
}

/**
 * Parameters for reflecting the schema.
 */
export interface ReflectSchemaOptions {
  definitionNameFilter?: string;
  caveatNameFilter?: string;
  relationNameFilter?: string;
  permissionNameFilter?: string;
}

/**
 * Parameters for computing computable permissions.
 */
export interface ComputablePermissionsParams {
  definitionName: string;
  relationName: string;
  definitionNameFilter?: string;
}

/**
 * Parameters for finding dependent relations.
 */
export interface DependentRelationsParams {
  definitionName: string;
  permissionName: string;
}

/**
 * A relation reference returned by schema reflection.
 */
export interface RelationReference {
  definitionName: string;
  relationName: string;
  isPermission: boolean;
}

// ---------------------------------------------------------------------------
// Expand tree (mirrors spicedb-go/client/expand_tree.go)
// ---------------------------------------------------------------------------

/**
 * The set operation combining an {@link IntermediateNode}'s children.
 */
export type TreeOperation =
  | "unspecified"
  | "union"
  | "intersection"
  | "exclusion";

/**
 * Identifies a resource or subject object.
 */
export interface ObjectRef {
  objectType: string;
  objectId: string;
}

/**
 * A subject with access at a leaf of a {@link PermissionTree}.
 */
export interface SubjectRef {
  subjectType: string;
  subjectId: string;
  optionalRelation: string;
}

/**
 * Combines child subtrees with a set operation.
 */
export interface IntermediateNode {
  operation: TreeOperation;
  children: PermissionTree[];
}

/**
 * Holds the concrete subjects at a leaf of a {@link PermissionTree}.
 */
export interface LeafNode {
  subjects: SubjectRef[];
}

/**
 * A native node of an expanded permission tree. Exactly one of
 * `intermediate` or `leaf` is set.
 */
export interface PermissionTree {
  expandedObject: ObjectRef;
  expandedRelation: string;
  intermediate?: IntermediateNode;
  leaf?: LeafNode;
}

// ---------------------------------------------------------------------------
// Schema reflection / diff (mirrors spicedb-go/client/schema.go)
// ---------------------------------------------------------------------------

/**
 * A parameter of a caveat.
 */
export interface SchemaCaveatParameter {
  name: string;
  type: string;
  parentCaveatName: string;
}

/**
 * A caveat defined in a SpiceDB schema.
 */
export interface SchemaCaveat {
  name: string;
  comment: string;
  expression: string;
  parameters: SchemaCaveatParameter[];
}

/**
 * A relation within a schema definition.
 */
export interface SchemaRelation {
  name: string;
  comment: string;
  parentDefinitionName: string;
}

/**
 * A permission within a schema definition.
 */
export interface SchemaPermission {
  name: string;
  comment: string;
  parentDefinitionName: string;
}

/**
 * A definition in a SpiceDB schema, including its relations and permissions.
 */
export interface SchemaDefinition {
  name: string;
  comment: string;
  relations: SchemaRelation[];
  permissions: SchemaPermission[];
}

/**
 * The result of a schema reflection call.
 */
export interface ReflectSchemaResult {
  definitions: SchemaDefinition[];
  caveats: SchemaCaveat[];
  revision: string;
}

/**
 * A single difference between two schemas.
 *
 * `kind` is a human-readable description of the diff type (e.g.
 * `"definition_added"`, `"relation_removed"`, `"permission_expr_changed"`)
 * and the associated fields contain the details:
 * - `definitionName` is set for definition and relation/permission-level diffs.
 * - `relationName` is set for relation-level diffs.
 * - `permissionName` is set for permission-level diffs.
 * - `caveatName` is set for caveat-level diffs.
 */
export interface SchemaDiff {
  kind: string;
  definitionName: string;
  relationName: string;
  permissionName: string;
  caveatName: string;
}

/**
 * Result of counting relationships.
 * @experimental
 */
export interface RelationshipCountResult {
  stillCalculating: boolean;
  count?: number;
  revision?: string;
}

/**
 * Transaction builder for batching relationship mutations.
 */
export class Transaction {
  /** @internal */
  readonly updates: ProtoRelationshipUpdate[] = [];
  /** @internal */
  readonly preconditions: ProtoPrecondition[] = [];
  /** @internal */
  metadata?: Record<string, unknown>;

  /**
   * Creates a relationship. Fails if it already exists.
   */
  create(rel: Relationship): this {
    this.updates.push(
      create(RelationshipUpdateSchema, {
        operation: RelationshipUpdate_Operation.CREATE,
        relationship: toProtoRelationship(rel),
      }),
    );
    return this;
  }

  /**
   * Upserts a relationship. Does not fail if it already exists.
   */
  touch(rel: Relationship): this {
    this.updates.push(
      create(RelationshipUpdateSchema, {
        operation: RelationshipUpdate_Operation.TOUCH,
        relationship: toProtoRelationship(rel),
      }),
    );
    return this;
  }

  /**
   * Deletes a relationship. No-ops if it doesn't exist.
   */
  delete(rel: Relationship): this {
    this.updates.push(
      create(RelationshipUpdateSchema, {
        operation: RelationshipUpdate_Operation.DELETE,
        relationship: toProtoRelationship(rel),
      }),
    );
    return this;
  }

  /**
   * Adds a precondition that no relationships match the given filter.
   * The transaction will fail if any matching relationships exist.
   */
  mustNotMatch(filter: RelationshipFilterOptions): this {
    this.preconditions.push(
      toProtoPrecondition(Precondition_Operation.MUST_NOT_MATCH, filter),
    );
    return this;
  }

  /**
   * Adds a precondition that at least one relationship matches the given filter.
   * The transaction will fail if no matching relationships exist.
   */
  mustMatch(filter: RelationshipFilterOptions): this {
    this.preconditions.push(
      toProtoPrecondition(Precondition_Operation.MUST_MATCH, filter),
    );
    return this;
  }

  /**
   * Sets optional transaction metadata that will be included in watch events.
   */
  withMetadata(meta: Record<string, unknown>): this {
    this.metadata = meta;
    return this;
  }
}

/**
 * Creates a Relationship from a "type:id" resource, relation, and "type:id" subject.
 *
 * @example
 * ```typescript
 * const rel = relationship("document:readme", "viewer", "user:jimmy");
 * ```
 */
export function relationship(
  resource: string,
  relation: string,
  subject: string,
): Relationship {
  const [resourceType, resourceId] = parseRef(resource, "resource");
  const [subjectType, subjectId, subjectRelation] = parseSubjectRef(subject);
  return {
    resourceType,
    resourceId,
    resourceRelation: relation,
    subjectType,
    subjectId,
    subjectRelation,
  };
}

/**
 * Creates a Relationship from a "type:id#relation" tuple string and a "type:id" subject.
 *
 * @example
 * ```typescript
 * const rel = relationshipFromTuple("document:readme#viewer", "user:jimmy");
 * ```
 */
export function relationshipFromTuple(
  resourceTuple: string,
  subject: string,
): Relationship {
  const hashIdx = resourceTuple.indexOf("#");
  if (hashIdx === -1) {
    throw new Error(
      `Invalid resource tuple: "${resourceTuple}" — expected "type:id#relation"`,
    );
  }
  const resourceRef = resourceTuple.substring(0, hashIdx);
  const relation = resourceTuple.substring(hashIdx + 1);
  return relationship(resourceRef, relation, subject);
}

function parseRef(ref: string, label: string): [string, string] {
  const colonIdx = ref.indexOf(":");
  if (colonIdx === -1) {
    throw new Error(
      `Invalid ${label} reference: "${ref}" — expected "type:id"`,
    );
  }
  return [ref.substring(0, colonIdx), ref.substring(colonIdx + 1)];
}

function parseSubjectRef(
  ref: string,
): [string, string, string | undefined] {
  const colonIdx = ref.indexOf(":");
  if (colonIdx === -1) {
    throw new Error(
      `Invalid subject reference: "${ref}" — expected "type:id" or "type:id#relation"`,
    );
  }
  const subjectType = ref.substring(0, colonIdx);
  const rest = ref.substring(colonIdx + 1);
  const hashIdx = rest.indexOf("#");
  if (hashIdx === -1) {
    return [subjectType, rest, undefined];
  }
  return [subjectType, rest.substring(0, hashIdx), rest.substring(hashIdx + 1)];
}

/** @internal */
export function toProtoRelationship(rel: Relationship): ProtoRelationship {
  const proto = create(RelationshipSchema, {
    resource: create(ObjectReferenceSchema, {
      objectType: rel.resourceType,
      objectId: rel.resourceId,
    }),
    relation: rel.resourceRelation,
    subject: create(SubjectReferenceSchema, {
      object: create(ObjectReferenceSchema, {
        objectType: rel.subjectType,
        objectId: rel.subjectId,
      }),
      optionalRelation: rel.subjectRelation ?? "",
    }),
  });

  if (rel.caveatName) {
    proto.optionalCaveat = create(ContextualizedCaveatSchema, {
      caveatName: rel.caveatName,
      // Internal cast: the proto Struct field requires JsonObject; the
      // public field is the native Record<string, unknown>.
      context: rel.caveatContext as JsonObject | undefined,
    });
  }

  if (rel.expiration) {
    proto.optionalExpiresAt = timestampFromDate(rel.expiration);
  }

  return proto;
}

/** @internal */
export function fromProtoRelationship(
  proto: ProtoRelationship,
): Relationship {
  const rel: Relationship = {
    resourceType: proto.resource?.objectType ?? "",
    resourceId: proto.resource?.objectId ?? "",
    resourceRelation: proto.relation,
    subjectType: proto.subject?.object?.objectType ?? "",
    subjectId: proto.subject?.object?.objectId ?? "",
  };

  if (proto.subject?.optionalRelation) {
    rel.subjectRelation = proto.subject.optionalRelation;
  }

  if (proto.optionalCaveat?.caveatName) {
    rel.caveatName = proto.optionalCaveat.caveatName;
    if (proto.optionalCaveat.context) {
      rel.caveatContext = proto.optionalCaveat.context;
    }
  }

  if (proto.optionalExpiresAt) {
    rel.expiration = new Date(
      Number(proto.optionalExpiresAt.seconds) * 1000 +
        proto.optionalExpiresAt.nanos / 1_000_000,
    );
  }

  return rel;
}

/** @internal */
export function toProtoRelationshipFilter(
  filter: RelationshipFilterOptions,
): ProtoRelationshipFilter {
  const proto = create(RelationshipFilterSchema, {
    resourceType: filter.resourceType,
    optionalResourceId: filter.resourceId ?? "",
    optionalResourceIdPrefix: filter.resourceIdPrefix ?? "",
    optionalRelation: filter.resourceRelation ?? "",
  });

  if (filter.subjectType) {
    const subjectFilter = create(SubjectFilterSchema, {
      subjectType: filter.subjectType,
      optionalSubjectId: filter.subjectId ?? "",
    });
    if (filter.subjectRelation) {
      subjectFilter.optionalRelation = create(
        SubjectFilter_RelationFilterSchema,
        {
          relation: filter.subjectRelation,
        },
      );
    }
    proto.optionalSubjectFilter = subjectFilter;
  }

  return proto;
}

/**
 * Builds a Precondition proto from an operation and filter. Shared by
 * {@link Transaction}'s `mustMatch`/`mustNotMatch` and
 * {@link toProtoDeletePreconditions} so both build preconditions identically.
 *
 * @internal
 */
export function toProtoPrecondition(
  operation: Precondition_Operation,
  filter: RelationshipFilterOptions,
): ProtoPrecondition {
  return create(PreconditionSchema, {
    operation,
    filter: toProtoRelationshipFilter(filter),
  });
}

/**
 * Builds the `optionalPreconditions` array for a delete request from
 * {@link DeleteOptions}: `mustMatch` filters first, then `mustNotMatch`
 * filters, mirroring spicedb-python's `delete_relationships`.
 *
 * @internal
 */
export function toProtoDeletePreconditions(
  options?: DeleteOptions,
): ProtoPrecondition[] {
  return [
    ...(options?.mustMatch ?? []).map((filter) =>
      toProtoPrecondition(Precondition_Operation.MUST_MATCH, filter),
    ),
    ...(options?.mustNotMatch ?? []).map((filter) =>
      toProtoPrecondition(Precondition_Operation.MUST_NOT_MATCH, filter),
    ),
  ];
}

/**
 * Builds a DeleteRelationshipsRequest proto from a filter and
 * {@link DeleteOptions}. `optionalAllowPartialDeletions` is set whenever
 * `limit` is provided — the server otherwise rejects a limited delete that
 * finds more matches than the limit. Exported for testing the request shape
 * directly; the client sends exactly what this returns.
 *
 * @internal
 */
export function toProtoDeleteRelationshipsRequest(
  filter: RelationshipFilterOptions,
  options?: DeleteOptions,
): ProtoDeleteRelationshipsRequest {
  return create(DeleteRelationshipsRequestSchema, {
    relationshipFilter: toProtoRelationshipFilter(filter),
    optionalPreconditions: toProtoDeletePreconditions(options),
    optionalLimit: options?.limit ?? 0,
    optionalAllowPartialDeletions: options?.limit !== undefined,
  });
}

// ---------------------------------------------------------------------------
// Expand tree mappers (mirror spicedb-go's toPermissionTree / toTreeOperation,
// client/expand_tree.go)
// ---------------------------------------------------------------------------

/** @internal */
export function toTreeOperation(
  op: AlgebraicSubjectSet_Operation,
): TreeOperation {
  switch (op) {
    case AlgebraicSubjectSet_Operation.UNION:
      return "union";
    case AlgebraicSubjectSet_Operation.INTERSECTION:
      return "intersection";
    case AlgebraicSubjectSet_Operation.EXCLUSION:
      return "exclusion";
    default:
      return "unspecified";
  }
}

/**
 * Recursively maps a proto PermissionRelationshipTree to its native
 * representation. An undefined input maps to a zero-value PermissionTree.
 * @internal
 */
export function fromProtoPermissionTree(
  t?: ProtoPermissionRelationshipTree,
): PermissionTree {
  if (!t) {
    return { expandedObject: { objectType: "", objectId: "" }, expandedRelation: "" };
  }

  const tree: PermissionTree = {
    expandedObject: {
      objectType: t.expandedObject?.objectType ?? "",
      objectId: t.expandedObject?.objectId ?? "",
    },
    expandedRelation: t.expandedRelation,
  };

  if (t.treeType.case === "intermediate") {
    tree.intermediate = {
      operation: toTreeOperation(t.treeType.value.operation),
      children: t.treeType.value.children.map((child) =>
        fromProtoPermissionTree(child),
      ),
    };
  }

  if (t.treeType.case === "leaf") {
    tree.leaf = {
      subjects: t.treeType.value.subjects.map((subject) => ({
        subjectType: subject.object?.objectType ?? "",
        subjectId: subject.object?.objectId ?? "",
        optionalRelation: subject.optionalRelation,
      })),
    };
  }

  return tree;
}

// ---------------------------------------------------------------------------
// Schema reflection / diff mappers (mirror spicedb-go's client/schema.go)
// ---------------------------------------------------------------------------

/** @internal */
export function fromProtoSchemaDefinition(
  def: ProtoReflectionDefinition,
): SchemaDefinition {
  return {
    name: def.name,
    comment: def.comment,
    relations: def.relations.map((rel) => ({
      name: rel.name,
      comment: rel.comment,
      parentDefinitionName: rel.parentDefinitionName,
    })),
    permissions: def.permissions.map((perm) => ({
      name: perm.name,
      comment: perm.comment,
      parentDefinitionName: perm.parentDefinitionName,
    })),
  };
}

/** @internal */
export function fromProtoSchemaCaveat(cav: ProtoReflectionCaveat): SchemaCaveat {
  return {
    name: cav.name,
    comment: cav.comment,
    expression: cav.expression,
    parameters: cav.parameters.map((param) => ({
      name: param.name,
      type: param.type,
      parentCaveatName: param.parentCaveatName,
    })),
  };
}

/** @internal */
export function fromProtoRelationReference(
  ref: ProtoReflectionRelationReference,
): RelationReference {
  return {
    definitionName: ref.definitionName,
    relationName: ref.relationName,
    isPermission: ref.isPermission,
  };
}

/**
 * Maps a single proto ReflectionSchemaDiff to its native representation.
 * Mirrors spicedb-go's `schemaDiffFromProto` (client/schema.go).
 * @internal
 */
export function fromProtoSchemaDiff(d: ProtoReflectionSchemaDiff): SchemaDiff {
  const base: SchemaDiff = {
    kind: "unknown",
    definitionName: "",
    relationName: "",
    permissionName: "",
    caveatName: "",
  };

  switch (d.diff.case) {
    case "definitionAdded":
      return { ...base, kind: "definition_added", definitionName: d.diff.value.name };
    case "definitionRemoved":
      return { ...base, kind: "definition_removed", definitionName: d.diff.value.name };
    case "definitionDocCommentChanged":
      return {
        ...base,
        kind: "definition_doc_comment_changed",
        definitionName: d.diff.value.name,
      };
    case "relationAdded":
      return {
        ...base,
        kind: "relation_added",
        definitionName: d.diff.value.parentDefinitionName,
        relationName: d.diff.value.name,
      };
    case "relationRemoved":
      return {
        ...base,
        kind: "relation_removed",
        definitionName: d.diff.value.parentDefinitionName,
        relationName: d.diff.value.name,
      };
    case "relationDocCommentChanged":
      return {
        ...base,
        kind: "relation_doc_comment_changed",
        definitionName: d.diff.value.parentDefinitionName,
        relationName: d.diff.value.name,
      };
    case "relationSubjectTypeAdded":
      return {
        ...base,
        kind: "relation_subject_type_added",
        definitionName: d.diff.value.relation?.parentDefinitionName ?? "",
        relationName: d.diff.value.relation?.name ?? "",
      };
    case "relationSubjectTypeRemoved":
      return {
        ...base,
        kind: "relation_subject_type_removed",
        definitionName: d.diff.value.relation?.parentDefinitionName ?? "",
        relationName: d.diff.value.relation?.name ?? "",
      };
    case "permissionAdded":
      return {
        ...base,
        kind: "permission_added",
        definitionName: d.diff.value.parentDefinitionName,
        permissionName: d.diff.value.name,
      };
    case "permissionRemoved":
      return {
        ...base,
        kind: "permission_removed",
        definitionName: d.diff.value.parentDefinitionName,
        permissionName: d.diff.value.name,
      };
    case "permissionDocCommentChanged":
      return {
        ...base,
        kind: "permission_doc_comment_changed",
        definitionName: d.diff.value.parentDefinitionName,
        permissionName: d.diff.value.name,
      };
    case "permissionExprChanged":
      return {
        ...base,
        kind: "permission_expr_changed",
        definitionName: d.diff.value.parentDefinitionName,
        permissionName: d.diff.value.name,
      };
    case "caveatAdded":
      return { ...base, kind: "caveat_added", caveatName: d.diff.value.name };
    case "caveatRemoved":
      return { ...base, kind: "caveat_removed", caveatName: d.diff.value.name };
    case "caveatDocCommentChanged":
      return {
        ...base,
        kind: "caveat_doc_comment_changed",
        caveatName: d.diff.value.name,
      };
    case "caveatExprChanged":
      return { ...base, kind: "caveat_expr_changed", caveatName: d.diff.value.name };
    case "caveatParameterAdded":
      return {
        ...base,
        kind: "caveat_parameter_added",
        caveatName: d.diff.value.parentCaveatName,
      };
    case "caveatParameterRemoved":
      return {
        ...base,
        kind: "caveat_parameter_removed",
        caveatName: d.diff.value.parentCaveatName,
      };
    case "caveatParameterTypeChanged":
      return {
        ...base,
        kind: "caveat_parameter_type_changed",
        caveatName: d.diff.value.parameter?.parentCaveatName ?? "",
      };
    default:
      return base;
  }
}

// ---------------------------------------------------------------------------
// Lookup results (mirrors spicedb-go's native lookup types and mappers,
// client/lookup_types.go: Permissionship, PartialCaveatInfo, LookupResource,
// ResolvedSubject, LookupSubject)
// ---------------------------------------------------------------------------

/**
 * Whether a check or lookup result reflects a full grant, a full denial, or
 * is conditional on caveat context that was not fully evaluated by the
 * server. Callers MUST check this before treating a result as a full grant —
 * a `"conditionalPermission"` result may resolve to false once the missing
 * caveat context is supplied.
 *
 * This type serves both the check surface ({@link CheckResult}) and the
 * lookup surface ({@link LookupResource}, {@link ResolvedSubject}). Lookups
 * never yield `"noPermission"`: a subject/resource pair that lacks the
 * permission is simply absent from a lookup stream rather than being yielded
 * with that permissionship. `"noPermission"` only appears on
 * {@link CheckResult}, where the server is answering a question about one
 * specific pair and "no" is itself an answer.
 *
 * `"noPermission"` was added after the three pre-existing values (not
 * inserted before them) so that any code depending on the historical
 * ordering of this union is unaffected. Mirrors spicedb-go's
 * `Permissionship`/`PermissionshipNoPermission` (client/lookup_types.go).
 */
export type Permissionship =
  | "unspecified"
  | "hasPermission"
  | "conditionalPermission"
  | "noPermission";

/**
 * Caveat context that was missing to fully evaluate a conditional result.
 */
export interface PartialCaveatInfo {
  missingRequiredContext: string[];
}

/**
 * One result from {@link SpiceDBClient.lookupResources}.
 */
export interface LookupResource {
  resourceId: string;
  permissionship: Permissionship;
  /** Set when `permissionship` is `"conditionalPermission"`. */
  partialCaveat?: PartialCaveatInfo;
  /**
   * The revision this result was computed at. Identical for every item
   * yielded by a single {@link SpiceDBClient.lookupResources} call — it is a
   * property of the call, not of the individual resource. Thread it into
   * `atLeast()`/`atLeastOrFull()` for read-your-writes on a later call.
   */
  lookedUpAt: string;
}

/**
 * A subject resolved by {@link SpiceDBClient.lookupSubjects} — either the
 * matched subject, or (when found in {@link LookupSubject.excludedSubjects})
 * a subject excluded from a wildcard match.
 */
export interface ResolvedSubject {
  subjectId: string;
  permissionship: Permissionship;
  partialCaveat?: PartialCaveatInfo;
}

/**
 * One result from {@link SpiceDBClient.lookupSubjects}. When
 * `subject.subjectId` is the wildcard `"*"`, `excludedSubjects` lists
 * subjects excluded from that wildcard grant — callers MUST treat those
 * subjects as NOT having the permission, even though the wildcard would
 * otherwise suggest they do.
 */
export interface LookupSubject {
  subject: ResolvedSubject;
  excludedSubjects: ResolvedSubject[];
  /**
   * The revision this result was computed at. Identical for every item
   * yielded by a single {@link SpiceDBClient.lookupSubjects} call — it is a
   * property of the call, not of the individual subject. Thread it into
   * `atLeast()`/`atLeastOrFull()` for read-your-writes on a later call.
   */
  lookedUpAt: string;
}

/**
 * Maps the proto `LookupPermissionship` enum to its native equivalent.
 * Unrecognized values map to `"unspecified"`. Mirrors spicedb-go's
 * `permissionshipFromProto` (client/lookup_types.go).
 * @internal
 */
export function permissionshipFromProto(
  v: LookupPermissionship,
): Permissionship {
  switch (v) {
    case LookupPermissionship.HAS_PERMISSION:
      return "hasPermission";
    case LookupPermissionship.CONDITIONAL_PERMISSION:
      return "conditionalPermission";
    default:
      return "unspecified";
  }
}

/**
 * Maps a proto `PartialCaveatInfo` to its native equivalent. An undefined
 * input maps to undefined. Mirrors spicedb-go's `partialCaveatFromProto`
 * (client/lookup_types.go).
 * @internal
 */
export function partialCaveatFromProto(
  v?: ProtoPartialCaveatInfo,
): PartialCaveatInfo | undefined {
  if (!v) {
    return undefined;
  }
  return { missingRequiredContext: v.missingRequiredContext };
}

/**
 * Maps a proto `ResolvedSubject` to its native equivalent. An undefined
 * input maps to a zero-value `ResolvedSubject` (empty `subjectId`), which
 * callers use as the trigger for falling back to deprecated response-level
 * fields. Mirrors spicedb-go's `resolvedSubjectFromProto`
 * (client/lookup_types.go).
 * @internal
 */
export function resolvedSubjectFromProto(
  v?: ProtoResolvedSubject,
): ResolvedSubject {
  return {
    subjectId: v?.subjectObjectId ?? "",
    permissionship: permissionshipFromProto(
      v?.permissionship ?? LookupPermissionship.UNSPECIFIED,
    ),
    partialCaveat: partialCaveatFromProto(v?.partialCaveatInfo),
  };
}

/**
 * Maps a proto `LookupResourcesResponse` to its native equivalent.
 * @internal
 */
export function fromProtoLookupResource(
  resp: ProtoLookupResourcesResponse,
): LookupResource {
  return {
    resourceId: resp.resourceObjectId,
    permissionship: permissionshipFromProto(resp.permissionship),
    partialCaveat: partialCaveatFromProto(resp.partialCaveatInfo),
    lookedUpAt: resp.lookedUpAt?.token ?? "",
  };
}

/**
 * Maps a proto `LookupSubjectsResponse` to its native equivalent, falling
 * back to the deprecated top-level `subjectObjectId`/`permissionship`/
 * `partialCaveatInfo` fields when `subject` is unset, and to the deprecated
 * `excludedSubjectIds` (IDs only, no permissionship/caveat info) when
 * `excludedSubjects` is empty. Mirrors spicedb-go's `LookupSubjects` loop
 * body (client/lookup.go).
 * @internal
 */
export function fromProtoLookupSubject(
  resp: ProtoLookupSubjectsResponse,
): LookupSubject {
  let subject = resolvedSubjectFromProto(resp.subject);
  if (!subject.subjectId) {
    // Fall back to the deprecated top-level fields for servers that don't
    // yet populate the non-deprecated `subject` field.
    subject = {
      subjectId: resp.subjectObjectId,
      permissionship: permissionshipFromProto(resp.permissionship),
      partialCaveat: partialCaveatFromProto(resp.partialCaveatInfo),
    };
  }

  let excludedSubjects: ResolvedSubject[] = [];
  if (resp.excludedSubjects.length > 0) {
    excludedSubjects = resp.excludedSubjects.map((e) =>
      resolvedSubjectFromProto(e),
    );
  } else if (resp.excludedSubjectIds.length > 0) {
    // Fall back to the deprecated excluded_subject_ids field, which carries
    // only IDs (no permissionship/caveat info).
    excludedSubjects = resp.excludedSubjectIds.map((id) => ({
      subjectId: id,
      permissionship: "unspecified" as const,
    }));
  }

  return { subject, excludedSubjects, lookedUpAt: resp.lookedUpAt?.token ?? "" };
}

// ---------------------------------------------------------------------------
// Check results (mirrors spicedb-go's native check types and mappers,
// client/check_types.go: CheckResult, checkPermissionshipFromProto,
// checkResultFromProto, checkResultFromBulkItem)
// ---------------------------------------------------------------------------

/**
 * The outcome of a permission check. `permissionship` carries the server's
 * answer; a `"conditionalPermission"` result means the server needed caveat
 * context that was not supplied in the check's `context` and is NOT a
 * grant — prefer {@link CheckResult.hasPermission} over comparing
 * `permissionship` directly for the common case.
 *
 * A class (rather than a plain object, unlike {@link LookupResource}) so
 * that `hasPermission()` travels with the data instead of requiring a
 * separate helper import at every call site — the same reasoning that makes
 * {@link Transaction} and `Consistency` classes elsewhere in this file.
 */
export class CheckResult {
  constructor(
    /** The server's answer. Prefer `hasPermission()` for the common case. */
    readonly permissionship: Permissionship,
    /**
     * Caveat context keys the server needed and did not receive. Empty
     * unless `permissionship` is `"conditionalPermission"`.
     */
    readonly missingContext: string[],
    /**
     * The revision this check was evaluated at. Thread it into
     * `atLeast()`/`atLeastOrFull()` to make a later read observe this check
     * (and everything it observed) — read-your-writes for checks.
     */
    readonly checkedAt: string,
  ) {}

  /**
   * Whether the subject has the permission outright. `false` for a
   * `"conditionalPermission"` result: the server could not evaluate the
   * caveat, so treating it as a grant would authorize on an unevaluated
   * condition. This is the ONLY case that returns `true` — `"noPermission"`,
   * `"unspecified"`, and `"conditionalPermission"` are all `false`.
   */
  hasPermission(): boolean {
    return this.permissionship === "hasPermission";
  }
}

/**
 * Merges a call-level default caveat context ({@link CheckOptions.context})
 * with a single check's own item-level context
 * ({@link CheckRequest.context}): a key-level merge where the item's own
 * value wins on conflict and default keys the item does not override are
 * retained — NOT a wholesale replacement, which would silently drop every
 * default key an item didn't happen to mention. Returns `undefined` (never
 * an empty object) when neither is supplied, so no context field is set on
 * the wire. Shared by {@link SpiceDBClient.checkPermission} and the
 * `CheckBulkPermissions`-backed surfaces (`checkPermissions`/`checkAny`/
 * `checkAll`) so both build the merged context identically.
 * @internal
 */
export function mergeCheckContext(
  defaultContext: Record<string, unknown> | undefined,
  itemContext: Record<string, unknown> | undefined,
): JsonObject | undefined {
  if (!defaultContext && !itemContext) {
    return undefined;
  }
  return { ...defaultContext, ...itemContext } as JsonObject;
}

/**
 * Maps the proto `CheckPermissionResponse.Permissionship` enum (which,
 * unlike `LookupPermissionship`, has a `NO_PERMISSION` value) to its native
 * equivalent. Unrecognized values map to `"unspecified"`. Mirrors
 * spicedb-go's `checkPermissionshipFromProto` (client/check_types.go).
 * @internal
 */
export function checkPermissionshipFromProto(
  v: CheckPermissionResponse_Permissionship,
): Permissionship {
  switch (v) {
    case CheckPermissionResponse_Permissionship.HAS_PERMISSION:
      return "hasPermission";
    case CheckPermissionResponse_Permissionship.CONDITIONAL_PERMISSION:
      return "conditionalPermission";
    case CheckPermissionResponse_Permissionship.NO_PERMISSION:
      return "noPermission";
    default:
      return "unspecified";
  }
}

/**
 * Maps a proto `CheckPermissionResponse` (the response from a single,
 * non-bulk `CheckPermission` RPC) to a native {@link CheckResult}. Uses the
 * response's own `checked_at` directly. Mirrors spicedb-go's
 * `checkResultFromProto` (client/check_types.go).
 * @internal
 */
export function checkResultFromProto(
  resp: ProtoCheckPermissionResponse,
): CheckResult {
  return new CheckResult(
    checkPermissionshipFromProto(resp.permissionship),
    resp.partialCaveatInfo?.missingRequiredContext ?? [],
    resp.checkedAt?.token ?? "",
  );
}

/**
 * Maps a proto `CheckBulkPermissionsResponseItem` (one pair's successful
 * result from a `CheckBulkPermissions` call) to a native {@link CheckResult}.
 * `CheckBulkPermissionsResponseItem` carries the same
 * permissionship/partial-caveat-info fields as `CheckPermissionResponse`,
 * but has no per-item `checked_at` of its own — the token lives once on the
 * enclosing `CheckBulkPermissionsResponse` and applies to every pair in it,
 * so callers pass it in as `responseCheckedAt` to propagate onto each item.
 * Mirrors spicedb-go's `checkResultFromBulkItem` (client/check_types.go).
 * @internal
 */
export function checkResultFromBulkItem(
  item: ProtoCheckBulkPermissionsResponseItem,
  responseCheckedAt: string,
): CheckResult {
  return new CheckResult(
    checkPermissionshipFromProto(item.permissionship),
    item.partialCaveatInfo?.missingRequiredContext ?? [],
    responseCheckedAt,
  );
}
