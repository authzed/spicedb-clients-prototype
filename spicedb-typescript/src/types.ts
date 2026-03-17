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
  caveatContext?: JsonObject;
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
 * Parameters for looking up resources.
 */
export interface LookupResourcesParams {
  resourceType: string;
  permission: string;
  subjectType: string;
  subjectId: string;
  subjectRelation?: string;
  context?: JsonObject;
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
  context?: JsonObject;
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
  context?: JsonObject;
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
  metadata?: JsonObject;
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
  metadata?: JsonObject;

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
      create(PreconditionSchema, {
        operation: Precondition_Operation.MUST_NOT_MATCH,
        filter: toProtoRelationshipFilter(filter),
      }),
    );
    return this;
  }

  /**
   * Adds a precondition that at least one relationship matches the given filter.
   * The transaction will fail if no matching relationships exist.
   */
  mustMatch(filter: RelationshipFilterOptions): this {
    this.preconditions.push(
      create(PreconditionSchema, {
        operation: Precondition_Operation.MUST_MATCH,
        filter: toProtoRelationshipFilter(filter),
      }),
    );
    return this;
  }

  /**
   * Sets optional transaction metadata that will be included in watch events.
   */
  withMetadata(meta: JsonObject): this {
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
      context: rel.caveatContext,
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
      rel.caveatContext = proto.optionalCaveat.context as JsonObject;
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
