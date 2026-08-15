import { create, type JsonObject } from "@bufbuild/protobuf";
import {
  createSpiceDBClient as createProtoClient,
  type ClientOptions as ProtoClientOptions,
  CheckPermissionResponse_Permissionship,
  CheckBulkPermissionsRequestItemSchema,
  CheckBulkPermissionsRequestSchema,
  CheckPermissionRequestSchema,
  ReadRelationshipsRequestSchema,
  WriteRelationshipsRequestSchema,
  DeleteRelationshipsRequestSchema,
  LookupResourcesRequestSchema,
  LookupSubjectsRequestSchema,
  ExpandPermissionTreeRequestSchema,
  ExportBulkRelationshipsRequestSchema,
  ImportBulkRelationshipsRequestSchema,
  ReadSchemaRequestSchema,
  WriteSchemaRequestSchema,
  ReflectSchemaRequestSchema,
  ReflectionSchemaFilterSchema,
  ComputablePermissionsRequestSchema,
  DependentRelationsRequestSchema,
  DiffSchemaRequestSchema,
  ExperimentalRegisterRelationshipCounterRequestSchema,
  ExperimentalCountRelationshipsRequestSchema,
  ExperimentalUnregisterRelationshipCounterRequestSchema,
  WatchRequestSchema,
  ObjectReferenceSchema,
  SubjectReferenceSchema,
  RelationshipSchema,
  ZedTokenSchema,
  RelationshipUpdate_Operation,
} from "@spicedb/proto";

import { Consistency } from "./consistency.js";

import {
  type Relationship,
  type RelationshipFilterOptions,
  type LookupResourcesParams,
  type LookupSubjectsParams,
  type LookupResource,
  type LookupSubject,
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
  type Transaction,
  type PermissionTree,
  type SchemaDefinition,
  type SchemaCaveat,
  type SchemaDiff,
  toProtoRelationship,
  fromProtoRelationship,
  toProtoRelationshipFilter,
  fromProtoPermissionTree,
  fromProtoSchemaDefinition,
  fromProtoSchemaCaveat,
  fromProtoRelationReference,
  fromProtoSchemaDiff,
  fromProtoLookupResource,
  fromProtoLookupSubject,
} from "./types.js";

import {
  toSpiceDBError,
  isTransientError,
} from "./errors.js";

/**
 * Options for creating a SpiceDBClient.
 */
export interface SpiceDBClientOptions {
  endpoint: string;
  token: string;
  insecure?: boolean;
  headers?: Record<string, string>;
  maxRetries?: number;
}

const DEFAULT_MAX_RETRIES = 3;

/**
 * SpiceDBClient provides an idiomatic TypeScript interface to SpiceDB.
 *
 * All read methods require an explicit consistency parameter.
 * All write methods return an opaque revision string.
 */
export class SpiceDBClient {
  private readonly proto: ReturnType<typeof createProtoClient>;
  private readonly maxRetries: number;

  constructor(options: SpiceDBClientOptions) {
    this.proto = createProtoClient(options.endpoint, options.token, {
      insecure: options.insecure,
      headers: options.headers,
    });
    this.maxRetries = options.maxRetries ?? DEFAULT_MAX_RETRIES;
  }

  // ---------------------------------------------------------------------------
  // Permission Checks
  // ---------------------------------------------------------------------------

  /**
   * Checks whether the subject has the given permission on the resource.
   *
   * @returns `true` if the subject has the permission, `false` otherwise.
   *          Caveated (conditional) permissions return `true`.
   */
  async checkPermission(
    consistency: Consistency,
    check: CheckRequest,
  ): Promise<boolean> {
    return this.withRetry(async () => {
      const resp = await this.proto.permissions.checkPermission(
        create(CheckPermissionRequestSchema, {
          consistency: consistency._toProto(),
          resource: create(ObjectReferenceSchema, {
            objectType: check.resourceType,
            objectId: check.resourceId,
          }),
          permission: check.permission,
          subject: create(SubjectReferenceSchema, {
            object: create(ObjectReferenceSchema, {
              objectType: check.subjectType,
              objectId: check.subjectId,
            }),
            optionalRelation: check.subjectRelation ?? "",
          }),
          context: check.context as JsonObject | undefined,
        }),
      );
      return (
        resp.permissionship ===
          CheckPermissionResponse_Permissionship.HAS_PERMISSION ||
        resp.permissionship ===
          CheckPermissionResponse_Permissionship.CONDITIONAL_PERMISSION
      );
    });
  }

  /**
   * Checks multiple permissions in a single bulk request.
   *
   * @returns An array of booleans corresponding to each check request.
   */
  async checkPermissions(
    consistency: Consistency,
    ...checks: CheckRequest[]
  ): Promise<boolean[]> {
    return this.withRetry(async () => {
      const resp = await this.proto.permissions.checkBulkPermissions(
        create(CheckBulkPermissionsRequestSchema, {
          consistency: consistency._toProto(),
          items: checks.map((check) =>
            create(CheckBulkPermissionsRequestItemSchema, {
              resource: create(ObjectReferenceSchema, {
                objectType: check.resourceType,
                objectId: check.resourceId,
              }),
              permission: check.permission,
              subject: create(SubjectReferenceSchema, {
                object: create(ObjectReferenceSchema, {
                  objectType: check.subjectType,
                  objectId: check.subjectId,
                }),
                optionalRelation: check.subjectRelation ?? "",
              }),
              context: check.context as JsonObject | undefined,
            }),
          ),
        }),
      );
      return resp.pairs.map((pair) => {
        if (pair.response.case === "error") {
          return false;
        }
        if (pair.response.case === "item") {
          return (
            pair.response.value.permissionship ===
              CheckPermissionResponse_Permissionship.HAS_PERMISSION ||
            pair.response.value.permissionship ===
              CheckPermissionResponse_Permissionship.CONDITIONAL_PERMISSION
          );
        }
        return false;
      });
    });
  }

  /**
   * Returns `true` if the subject has ANY of the specified permissions.
   */
  async checkAny(
    consistency: Consistency,
    ...checks: CheckRequest[]
  ): Promise<boolean> {
    const results = await this.checkPermissions(consistency, ...checks);
    return results.some((r) => r);
  }

  /**
   * Returns `true` if the subject has ALL of the specified permissions.
   */
  async checkAll(
    consistency: Consistency,
    ...checks: CheckRequest[]
  ): Promise<boolean> {
    const results = await this.checkPermissions(consistency, ...checks);
    return results.every((r) => r);
  }

  // ---------------------------------------------------------------------------
  // Relationship Reads
  // ---------------------------------------------------------------------------

  /**
   * Reads relationships matching the given filter.
   *
   * @returns An async iterable of matching relationships.
   */
  async *readRelationships(
    filter: RelationshipFilterOptions,
    consistency: Consistency,
  ): AsyncIterableIterator<Relationship> {
    const stream = this.proto.permissions.readRelationships(
      create(ReadRelationshipsRequestSchema, {
        consistency: consistency._toProto(),
        relationshipFilter: toProtoRelationshipFilter(filter),
      }),
    );
    try {
      for await (const resp of stream) {
        if (resp.relationship) {
          yield fromProtoRelationship(resp.relationship);
        }
      }
    } catch (err) {
      throw toSpiceDBError(err);
    }
  }

  // ---------------------------------------------------------------------------
  // Writes
  // ---------------------------------------------------------------------------

  /**
   * Writes relationships as a single atomic transaction.
   *
   * @returns The revision at which the write was committed.
   */
  async write(txn: Transaction): Promise<string> {
    return this.withRetry(async () => {
      const resp = await this.proto.permissions.writeRelationships(
        create(WriteRelationshipsRequestSchema, {
          updates: txn.updates,
          optionalPreconditions: txn.preconditions,
          optionalTransactionMetadata: txn.metadata as JsonObject | undefined,
        }),
      );
      return resp.writtenAt?.token ?? "";
    });
  }

  /**
   * Deletes all relationships matching the given filter.
   *
   * @returns The revision at which the deletion was committed.
   */
  async deleteRelationships(
    filter: RelationshipFilterOptions,
  ): Promise<string> {
    return this.withRetry(async () => {
      const resp = await this.proto.permissions.deleteRelationships(
        create(DeleteRelationshipsRequestSchema, {
          relationshipFilter: toProtoRelationshipFilter(filter),
        }),
      );
      return resp.deletedAt?.token ?? "";
    });
  }

  // ---------------------------------------------------------------------------
  // Lookups
  // ---------------------------------------------------------------------------

  /**
   * Looks up all resources of the given type that the subject has the
   * specified permission on. Each result carries the permissionship (full
   * grant vs conditional on caveat context) and, for conditional results,
   * which caveat context was missing. Callers MUST check `permissionship`
   * before treating a result as a full grant.
   *
   * @returns An async iterable of {@link LookupResource}.
   */
  async *lookupResources(
    params: LookupResourcesParams,
    consistency: Consistency,
  ): AsyncIterableIterator<LookupResource> {
    const stream = this.proto.permissions.lookupResources(
      create(LookupResourcesRequestSchema, {
        consistency: consistency._toProto(),
        resourceObjectType: params.resourceType,
        permission: params.permission,
        subject: create(SubjectReferenceSchema, {
          object: create(ObjectReferenceSchema, {
            objectType: params.subjectType,
            objectId: params.subjectId,
          }),
          optionalRelation: params.subjectRelation ?? "",
        }),
        context: params.context as JsonObject | undefined,
        optionalLimit: params.limit ?? 0,
      }),
    );
    try {
      for await (const resp of stream) {
        yield fromProtoLookupResource(resp);
      }
    } catch (err) {
      throw toSpiceDBError(err);
    }
  }

  /**
   * Looks up all subjects of the given type that have the specified
   * permission on the resource.
   *
   * When a yielded `LookupSubject.subject` is the wildcard `"*"`, the server
   * has granted the permission to every subject of `subjectType` EXCEPT
   * those listed in `LookupSubject.excludedSubjects`. Callers MUST check
   * `excludedSubjects` before treating a wildcard match as a blanket grant,
   * or they risk granting access to subjects the server explicitly excluded.
   *
   * @returns An async iterable of {@link LookupSubject}.
   */
  async *lookupSubjects(
    params: LookupSubjectsParams,
    consistency: Consistency,
  ): AsyncIterableIterator<LookupSubject> {
    const stream = this.proto.permissions.lookupSubjects(
      create(LookupSubjectsRequestSchema, {
        consistency: consistency._toProto(),
        resource: create(ObjectReferenceSchema, {
          objectType: params.resourceType,
          objectId: params.resourceId,
        }),
        permission: params.permission,
        subjectObjectType: params.subjectType,
        optionalSubjectRelation: params.subjectRelation ?? "",
        context: params.context as JsonObject | undefined,
        optionalConcreteLimit: params.limit ?? 0,
      }),
    );
    try {
      for await (const resp of stream) {
        yield fromProtoLookupSubject(resp);
      }
    } catch (err) {
      throw toSpiceDBError(err);
    }
  }

  // ---------------------------------------------------------------------------
  // Expand
  // ---------------------------------------------------------------------------

  /**
   * Expands a permission tree for the given resource and permission.
   */
  async expandPermissionTree(
    consistency: Consistency,
    params: ExpandPermissionTreeParams,
  ): Promise<{ expandedAt: string; treeRoot: PermissionTree }> {
    return this.withRetry(async () => {
      const resp = await this.proto.permissions.expandPermissionTree(
        create(ExpandPermissionTreeRequestSchema, {
          consistency: consistency._toProto(),
          resource: create(ObjectReferenceSchema, {
            objectType: params.resourceType,
            objectId: params.resourceId,
          }),
          permission: params.permission,
        }),
      );
      return {
        expandedAt: resp.expandedAt?.token ?? "",
        treeRoot: fromProtoPermissionTree(resp.treeRoot),
      };
    });
  }

  // ---------------------------------------------------------------------------
  // Bulk Operations
  // ---------------------------------------------------------------------------

  /**
   * Imports relationships in bulk. Pass an array of relationships to import
   * them all in a single transaction.
   *
   * @returns The number of relationships loaded.
   */
  async importBulkRelationships(
    relationships: Relationship[],
  ): Promise<bigint> {
    return this.withRetry(async () => {
      const protoRels = relationships.map((rel) => toProtoRelationship(rel));
      const resp = await this.proto.permissions.importBulkRelationships(
        // Client streaming: send all relationships in batches
        (async function* () {
          // Send in chunks of 1000 for efficiency
          const chunkSize = 1000;
          for (let i = 0; i < protoRels.length; i += chunkSize) {
            yield {
              relationships: protoRels.slice(i, i + chunkSize),
            };
          }
        })(),
      );
      return resp.numLoaded;
    });
  }

  /**
   * Exports all relationships, optionally filtered, as an async iterable.
   *
   * @returns An async iterable of relationships.
   */
  async *exportBulkRelationships(
    consistency: Consistency,
    filter?: RelationshipFilterOptions,
  ): AsyncIterableIterator<Relationship> {
    const stream = this.proto.permissions.exportBulkRelationships(
      create(ExportBulkRelationshipsRequestSchema, {
        consistency: consistency._toProto(),
        optionalRelationshipFilter: filter
          ? toProtoRelationshipFilter(filter)
          : undefined,
      }),
    );
    try {
      for await (const resp of stream) {
        for (const protoRel of resp.relationships) {
          yield fromProtoRelationship(protoRel);
        }
      }
    } catch (err) {
      throw toSpiceDBError(err);
    }
  }

  // ---------------------------------------------------------------------------
  // Schema
  // ---------------------------------------------------------------------------

  /**
   * Reads the current schema.
   *
   * @returns The schema text and revision.
   */
  async readSchema(): Promise<{ schema: string; revision: string }> {
    return this.withRetry(async () => {
      const resp = await this.proto.schema.readSchema(
        create(ReadSchemaRequestSchema, {}),
      );
      return {
        schema: resp.schemaText,
        revision: resp.readAt?.token ?? "",
      };
    });
  }

  /**
   * Writes (replaces) the current schema.
   *
   * @returns The revision at which the schema was written.
   */
  async writeSchema(schema: string): Promise<string> {
    return this.withRetry(async () => {
      const resp = await this.proto.schema.writeSchema(
        create(WriteSchemaRequestSchema, { schema }),
      );
      return resp.writtenAt?.token ?? "";
    });
  }

  /**
   * Reflects the schema, returning definitions and caveats.
   */
  async reflectSchema(
    consistency: Consistency,
    options?: ReflectSchemaOptions,
  ): Promise<{
    definitions: SchemaDefinition[];
    caveats: SchemaCaveat[];
    revision: string;
  }> {
    return this.withRetry(async () => {
      const filters = options
        ? [
            create(ReflectionSchemaFilterSchema, {
              optionalDefinitionNameFilter:
                options.definitionNameFilter ?? "",
              optionalCaveatNameFilter: options.caveatNameFilter ?? "",
              optionalRelationNameFilter:
                options.relationNameFilter ?? "",
              optionalPermissionNameFilter:
                options.permissionNameFilter ?? "",
            }),
          ]
        : [];

      const resp = await this.proto.schema.reflectSchema(
        create(ReflectSchemaRequestSchema, {
          consistency: consistency._toProto(),
          optionalFilters: filters,
        }),
      );
      return {
        definitions: resp.definitions.map((def) => fromProtoSchemaDefinition(def)),
        caveats: resp.caveats.map((cav) => fromProtoSchemaCaveat(cav)),
        revision: resp.readAt?.token ?? "",
      };
    });
  }

  /**
   * Computes the permissions that are computable for a given relation.
   */
  async computablePermissions(
    consistency: Consistency,
    params: ComputablePermissionsParams,
  ): Promise<{ permissions: RelationReference[]; revision: string }> {
    return this.withRetry(async () => {
      const resp = await this.proto.schema.computablePermissions(
        create(ComputablePermissionsRequestSchema, {
          consistency: consistency._toProto(),
          definitionName: params.definitionName,
          relationName: params.relationName,
          optionalDefinitionNameFilter: params.definitionNameFilter ?? "",
        }),
      );
      return {
        permissions: resp.permissions.map((p) => fromProtoRelationReference(p)),
        revision: resp.readAt?.token ?? "",
      };
    });
  }

  /**
   * Finds the relations that a permission depends on.
   */
  async dependentRelations(
    consistency: Consistency,
    params: DependentRelationsParams,
  ): Promise<{ relations: RelationReference[]; revision: string }> {
    return this.withRetry(async () => {
      const resp = await this.proto.schema.dependentRelations(
        create(DependentRelationsRequestSchema, {
          consistency: consistency._toProto(),
          definitionName: params.definitionName,
          permissionName: params.permissionName,
        }),
      );
      return {
        relations: resp.relations.map((r) => fromProtoRelationReference(r)),
        revision: resp.readAt?.token ?? "",
      };
    });
  }

  /**
   * Computes the diff between the current schema and a comparison schema.
   */
  async diffSchema(
    consistency: Consistency,
    comparisonSchema: string,
  ): Promise<{ diffs: SchemaDiff[]; revision: string }> {
    return this.withRetry(async () => {
      const resp = await this.proto.schema.diffSchema(
        create(DiffSchemaRequestSchema, {
          consistency: consistency._toProto(),
          comparisonSchema,
        }),
      );
      return {
        diffs: resp.diffs.map((d) => fromProtoSchemaDiff(d)),
        revision: resp.readAt?.token ?? "",
      };
    });
  }

  // ---------------------------------------------------------------------------
  // Experimental: Relationship Counters
  // ---------------------------------------------------------------------------

  /**
   * Registers a new relationship counter with the given filter.
   * @experimental This API may change without following backwards compatibility rules.
   */
  async experimentalRegisterRelationshipCounter(
    name: string,
    filter: RelationshipFilterOptions,
  ): Promise<void> {
    return this.withRetry(async () => {
      await this.proto.experimental.experimentalRegisterRelationshipCounter(
        create(ExperimentalRegisterRelationshipCounterRequestSchema, {
          name,
          relationshipFilter: toProtoRelationshipFilter(filter),
        }),
      );
    });
  }

  /**
   * Returns the count of relationships for a pre-registered counter.
   * @experimental This API may change without following backwards compatibility rules.
   */
  async experimentalCountRelationships(
    name: string,
  ): Promise<RelationshipCountResult> {
    return this.withRetry(async () => {
      const resp =
        await this.proto.experimental.experimentalCountRelationships(
          create(ExperimentalCountRelationshipsRequestSchema, { name }),
        );
      if (resp.counterResult.case === "counterStillCalculating") {
        return { stillCalculating: true };
      }
      if (resp.counterResult.case === "readCounterValue") {
        return {
          stillCalculating: false,
          count: Number(resp.counterResult.value.relationshipCount),
          revision: resp.counterResult.value.readAt?.token ?? "",
        };
      }
      return { stillCalculating: true };
    });
  }

  /**
   * Unregisters a previously registered relationship counter.
   * @experimental This API may change without following backwards compatibility rules.
   */
  async experimentalUnregisterRelationshipCounter(name: string): Promise<void> {
    return this.withRetry(async () => {
      await this.proto.experimental.experimentalUnregisterRelationshipCounter(
        create(ExperimentalUnregisterRelationshipCounterRequestSchema, {
          name,
        }),
      );
    });
  }

  // ---------------------------------------------------------------------------
  // Watch
  // ---------------------------------------------------------------------------

  /**
   * Watches for changes to relationships, returning an async iterable of events.
   */
  async *watch(
    options?: WatchOptions,
  ): AsyncIterableIterator<WatchEvent> {
    const req = create(WatchRequestSchema, {
      optionalObjectTypes: options?.objectTypes ?? [],
    });
    if (options?.startRevision) {
      req.optionalStartCursor = create(ZedTokenSchema, {
        token: options.startRevision,
      });
    }

    const stream = this.proto.watch.watch(req);
    try {
      for await (const resp of stream) {
        const changes: WatchChange[] = resp.updates.map((update) => {
          let operation: WatchChange["operation"];
          switch (update.operation) {
            case RelationshipUpdate_Operation.CREATE:
              operation = "create";
              break;
            case RelationshipUpdate_Operation.DELETE:
              operation = "delete";
              break;
            default:
              operation = "touch";
              break;
          }
          return {
            operation,
            relationship: update.relationship
              ? fromProtoRelationship(update.relationship)
              : {
                  resourceType: "",
                  resourceId: "",
                  resourceRelation: "",
                  subjectType: "",
                  subjectId: "",
                },
          };
        });

        yield {
          changes,
          revision: resp.changesThrough?.token ?? "",
          metadata: resp.optionalTransactionMetadata,
          schemaUpdated: resp.schemaUpdated,
          isCheckpoint: resp.isCheckpoint,
        };
      }
    } catch (err) {
      throw toSpiceDBError(err);
    }
  }

  // ---------------------------------------------------------------------------
  // Retry Logic
  // ---------------------------------------------------------------------------

  private async withRetry<T>(fn: () => Promise<T>): Promise<T> {
    let lastErr: unknown;
    for (let attempt = 0; attempt <= this.maxRetries; attempt++) {
      try {
        return await fn();
      } catch (err) {
        lastErr = err;
        if (!isTransientError(err) || attempt === this.maxRetries) {
          throw toSpiceDBError(err);
        }
        const delay = Math.min(100 * 2 ** attempt, 5000);
        await new Promise((resolve) => setTimeout(resolve, delay));
      }
    }
    throw toSpiceDBError(lastErr);
  }
}

/**
 * Creates a SpiceDBClient connected to the given endpoint.
 *
 * @param endpoint - The SpiceDB server address (host:port)
 * @param token - Bearer token for authentication
 * @param options - Optional configuration
 */
export function createSpiceDBClient(
  endpoint: string,
  token: string,
  options?: {
    insecure?: boolean;
    headers?: Record<string, string>;
    maxRetries?: number;
  },
): SpiceDBClient {
  return new SpiceDBClient({
    endpoint,
    token,
    ...options,
  });
}
