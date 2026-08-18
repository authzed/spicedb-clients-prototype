import { create, type JsonObject } from "@bufbuild/protobuf";
import {
  createSpiceDBClient as createProtoClient,
  type ClientOptions as ProtoClientOptions,
  CheckBulkPermissionsRequestItemSchema,
  CheckBulkPermissionsRequestSchema,
  CheckPermissionRequestSchema,
  ReadRelationshipsRequestSchema,
  WriteRelationshipsRequestSchema,
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
  type CheckOptions,
  CheckResult,
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
  type DeleteOptions,
  type PermissionTree,
  type SchemaDefinition,
  type SchemaCaveat,
  type SchemaDiff,
  toProtoRelationship,
  fromProtoRelationship,
  toProtoRelationshipFilter,
  toProtoDeleteRelationshipsRequest,
  fromProtoPermissionTree,
  fromProtoSchemaDefinition,
  fromProtoSchemaCaveat,
  fromProtoRelationReference,
  fromProtoSchemaDiff,
  fromProtoLookupResource,
  fromProtoLookupSubject,
  checkResultFromProto,
  checkResultFromBulkItem,
  mergeCheckContext,
} from "./types.js";

import {
  SpiceDBError,
  toSpiceDBError,
  toSpiceDBErrorFromStatus,
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
 * Normalizes the two calling conventions shared by `checkPermissions`,
 * `checkAny`, and `checkAll` into a plain `{ checks, options }` pair:
 *
 * - Classic variadic form — `(consistency, check1, check2, ...)` — every
 *   positional argument after `consistency` is an individual
 *   {@link CheckRequest}. Distinguished from the array form purely by
 *   `Array.isArray`: a real `CheckRequest` is never an array, so this can
 *   never misfire on an existing call site.
 * - Explicit-array form — `(consistency, checks, options?)` — `checksOrFirst`
 *   is the full `CheckRequest[]` and `optionsOrSecond`, if present, is
 *   {@link CheckOptions}.
 *
 * This keeps the pre-existing rest-parameter overload's behavior byte-for-
 * byte unchanged (no `options` is ever produced for it), while giving the
 * array form a place to carry a call-level default context.
 * @internal
 */
function normalizeBulkCheckArgs(
  checksOrFirst: CheckRequest[] | CheckRequest | undefined,
  optionsOrSecond: CheckOptions | CheckRequest | undefined,
  rest: CheckRequest[],
): { checks: CheckRequest[]; options?: CheckOptions } {
  if (Array.isArray(checksOrFirst)) {
    return { checks: checksOrFirst, options: optionsOrSecond as CheckOptions | undefined };
  }
  const checks: CheckRequest[] = [];
  if (checksOrFirst !== undefined) {
    checks.push(checksOrFirst);
  }
  if (optionsOrSecond !== undefined) {
    checks.push(optionsOrSecond as CheckRequest);
  }
  checks.push(...rest);
  return { checks };
}

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
   * Uses the single-check `CheckPermission` RPC directly (not routed
   * through `CheckBulkPermissions`).
   *
   * `options.context`, when supplied, is a call-level default caveat
   * context. It is merged key-by-key with `check.context` — `check.context`
   * wins on conflict, and default keys `check.context` does not mention are
   * retained (see {@link CheckOptions.context} for the exact rule). For a
   * single check this mostly reads as "either supplies context," but it
   * keeps the same `CheckOptions` shape usable across all four check
   * surfaces.
   *
   * @returns A {@link CheckResult}. Use `result.hasPermission()` for the
   *          common case — it is `true` ONLY when the server's answer is an
   *          unconditional grant. A `"conditionalPermission"` result means
   *          the server needed caveat context that was not supplied in
   *          `check.context`/`options.context`; it is NOT a grant, and
   *          `result.hasPermission()` returns `false` for it.
   */
  async checkPermission(
    consistency: Consistency,
    check: CheckRequest,
    options?: CheckOptions,
  ): Promise<CheckResult> {
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
          context: mergeCheckContext(options?.context, check.context),
        }),
      );
      return checkResultFromProto(resp);
    });
  }

  /**
   * Checks multiple permissions in a single bulk request.
   *
   * Two calling conventions:
   * - The classic variadic form (`consistency, check1, check2, ...`) —
   *   unchanged, and it never sets a call-level default.
   * - An explicit-array form (`consistency, checks, options`) that
   *   additionally accepts {@link CheckOptions}. `options.context` is a
   *   call-level default caveat context fanned out onto every item (the
   *   wire has no request-level context field — see
   *   {@link CheckOptions.context} for the exact per-item merge rule with
   *   each check's own `context`).
   *
   * A per-item failure (e.g. an internal error evaluating one specific
   * check) is surfaced by throwing a typed {@link SpiceDBError} — it is
   * NEVER silently coerced into a result, since that would make a
   * permission-denied, an invalid-argument, and an internal server error
   * all indistinguishable from a real "no permission" answer.
   *
   * @returns An array of {@link CheckResult}, one per check request, in the
   *          same order. Every result shares the same `checkedAt` — the
   *          response carries a single token for the whole batch, not one
   *          per item.
   */
  async checkPermissions(
    consistency: Consistency,
    ...checks: CheckRequest[]
  ): Promise<CheckResult[]>;
  async checkPermissions(
    consistency: Consistency,
    checks: CheckRequest[],
    options?: CheckOptions,
  ): Promise<CheckResult[]>;
  async checkPermissions(
    consistency: Consistency,
    checksOrFirst?: CheckRequest[] | CheckRequest,
    optionsOrSecond?: CheckOptions | CheckRequest,
    ...rest: CheckRequest[]
  ): Promise<CheckResult[]> {
    const { checks, options } = normalizeBulkCheckArgs(
      checksOrFirst,
      optionsOrSecond,
      rest,
    );
    return this.runBulkCheck(consistency, checks, options);
  }

  /**
   * Shared implementation behind {@link checkPermissions}, {@link checkAny},
   * and {@link checkAll} — all three are aggregates/pass-throughs over the
   * same `CheckBulkPermissions` request, so the request-building and
   * response-mapping logic (including the call-level `options.context`
   * merge) lives here once.
   * @internal
   */
  private async runBulkCheck(
    consistency: Consistency,
    checks: CheckRequest[],
    options?: CheckOptions,
  ): Promise<CheckResult[]> {
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
              context: mergeCheckContext(options?.context, check.context),
            }),
          ),
        }),
      );
      // The proto guarantees pairs are returned in request order but says
      // nothing about count. A short response would otherwise silently
      // desync results[i] from checks[i] for every item after the gap — one
      // resource's answer attributed to another — and `checkAll`'s
      // `.every()` would return `true` over an array missing the checks that
      // would have denied. Fail loudly instead of returning a
      // misaligned-but-"successful" array. The doc comment on
      // `checkPermissions` promises "one per check request, in the same
      // order"; this is what enforces it.
      if (resp.pairs.length !== checks.length) {
        throw new SpiceDBError(
          `checkBulkPermissions returned ${resp.pairs.length} pair(s) for ` +
            `${checks.length} request item(s)`,
        );
      }

      const checkedAt = resp.checkedAt?.token ?? "";
      return resp.pairs.map((pair, i) => {
        if (pair.response.case === "error") {
          // A per-item error MUST reach the caller as a typed error, not as
          // a falsy/failing CheckResult — see the doc comment above.
          throw toSpiceDBErrorFromStatus(pair.response.value);
        }
        if (pair.response.case === "item") {
          return checkResultFromBulkItem(pair.response.value, checkedAt);
        }
        // Malformed pair: the oneof has neither `item` nor `error` set. A
        // well-behaved server always sets one, so this should be unreachable
        // in practice. Throw, matching the other six clients' guard, rather
        // than degrading to an `unspecified` result: `unspecified` is
        // non-granting and `.map()` does preserve index alignment, so the
        // desync rationale above doesn't apply here — but it is
        // indistinguishable from a real server answer of "no permission",
        // which hides a broken server behind a plausible-looking denial.
        throw new SpiceDBError(
          `check item ${i}: malformed CheckBulkPermissionsPair ` +
            `(neither item nor error set)`,
        );
      });
    });
  }

  /**
   * Returns `true` if the subject has ANY of the specified permissions
   * outright. A `"conditionalPermission"` result does NOT count as
   * granted — only results where {@link CheckResult.hasPermission} is
   * `true` are considered. This is deliberate and fail-closed.
   *
   * Accepts the same two calling conventions as `checkPermissions`,
   * including the explicit-array form with a call-level {@link CheckOptions}
   * default.
   */
  async checkAny(
    consistency: Consistency,
    ...checks: CheckRequest[]
  ): Promise<boolean>;
  async checkAny(
    consistency: Consistency,
    checks: CheckRequest[],
    options?: CheckOptions,
  ): Promise<boolean>;
  async checkAny(
    consistency: Consistency,
    checksOrFirst?: CheckRequest[] | CheckRequest,
    optionsOrSecond?: CheckOptions | CheckRequest,
    ...rest: CheckRequest[]
  ): Promise<boolean> {
    const { checks, options } = normalizeBulkCheckArgs(
      checksOrFirst,
      optionsOrSecond,
      rest,
    );
    const results = await this.runBulkCheck(consistency, checks, options);
    return results.some((r) => r.hasPermission());
  }

  /**
   * Returns `true` if the subject has ALL of the specified permissions
   * outright. A `"conditionalPermission"` result does NOT count as
   * granted — every result must satisfy {@link CheckResult.hasPermission}
   * for this to return `true`. This is deliberate and fail-closed.
   *
   * Accepts the same two calling conventions as `checkPermissions`,
   * including the explicit-array form with a call-level {@link CheckOptions}
   * default.
   *
   * Returns `false`, not the vacuous `true` that `Array.prototype.every`
   * yields on an empty array, when zero checks are given — "no checks to
   * run" is not "all checks passed".
   */
  async checkAll(
    consistency: Consistency,
    ...checks: CheckRequest[]
  ): Promise<boolean>;
  async checkAll(
    consistency: Consistency,
    checks: CheckRequest[],
    options?: CheckOptions,
  ): Promise<boolean>;
  async checkAll(
    consistency: Consistency,
    checksOrFirst?: CheckRequest[] | CheckRequest,
    optionsOrSecond?: CheckOptions | CheckRequest,
    ...rest: CheckRequest[]
  ): Promise<boolean> {
    const { checks, options } = normalizeBulkCheckArgs(
      checksOrFirst,
      optionsOrSecond,
      rest,
    );
    if (checks.length === 0) {
      return false;
    }
    const results = await this.runBulkCheck(consistency, checks, options);
    return results.every((r) => r.hasPermission());
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
    const request = create(ReadRelationshipsRequestSchema, {
      consistency: consistency._toProto(),
      relationshipFilter: toProtoRelationshipFilter(filter),
    });
    let attempt = 0;
    for (;;) {
      let yielded = 0;
      try {
        const stream = this.proto.permissions.readRelationships(request);
        for await (const resp of stream) {
          if (resp.relationship) {
            yield fromProtoRelationship(resp.relationship);
            yielded++;
          }
        }
        return;
      } catch (err) {
        if (
          yielded === 0 &&
          (await this.shouldRetryEstablishment(attempt, err))
        ) {
          attempt++;
          continue;
        }
        throw toSpiceDBError(err);
      }
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
   * `options.mustMatch`/`options.mustNotMatch` add preconditions that guard
   * the delete: if a precondition fails, the server rejects the call and
   * deletes nothing. Mirrors spicedb-go's `WithDeleteMustMatch`/
   * `WithDeleteMustNotMatch` (client/relationships.go).
   *
   * `options.limit` bounds how many relationships this call deletes. If
   * more relationships match the filter than `limit`, only `limit` of them
   * are deleted by this call (the server requires
   * `optionalAllowPartialDeletions`, which this sets automatically whenever
   * `limit` is given, to permit that). Unlike spicedb-go's
   * `WithDeleteLimit`, this does not auto-page — it does not loop to delete
   * every match when the match count exceeds `limit`; call again with the
   * same filter to continue deleting what remains.
   *
   * @returns The revision at which the deletion was committed.
   */
  async deleteRelationships(
    filter: RelationshipFilterOptions,
    options?: DeleteOptions,
  ): Promise<string> {
    return this.withRetry(async () => {
      const resp = await this.proto.permissions.deleteRelationships(
        toProtoDeleteRelationshipsRequest(filter, options),
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
   * grant vs conditional on caveat context), and, for conditional results,
   * which caveat context was missing, and `lookedUpAt` — the revision the
   * result was computed at. Callers MUST check `permissionship` before
   * treating a result as a full grant.
   *
   * @returns An async iterable of {@link LookupResource}.
   */
  async *lookupResources(
    params: LookupResourcesParams,
    consistency: Consistency,
  ): AsyncIterableIterator<LookupResource> {
    const request = create(LookupResourcesRequestSchema, {
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
    });
    let attempt = 0;
    for (;;) {
      let yielded = 0;
      try {
        const stream = this.proto.permissions.lookupResources(request);
        for await (const resp of stream) {
          yield fromProtoLookupResource(resp);
          yielded++;
        }
        return;
      } catch (err) {
        if (
          yielded === 0 &&
          (await this.shouldRetryEstablishment(attempt, err))
        ) {
          attempt++;
          continue;
        }
        throw toSpiceDBError(err);
      }
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
   * `lookedUpAt` carries the revision the result was computed at.
   *
   * @returns An async iterable of {@link LookupSubject}.
   */
  async *lookupSubjects(
    params: LookupSubjectsParams,
    consistency: Consistency,
  ): AsyncIterableIterator<LookupSubject> {
    const request = create(LookupSubjectsRequestSchema, {
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
    });
    let attempt = 0;
    for (;;) {
      let yielded = 0;
      try {
        const stream = this.proto.permissions.lookupSubjects(request);
        for await (const resp of stream) {
          yield fromProtoLookupSubject(resp);
          yielded++;
        }
        return;
      } catch (err) {
        if (
          yielded === 0 &&
          (await this.shouldRetryEstablishment(attempt, err))
        ) {
          attempt++;
          continue;
        }
        throw toSpiceDBError(err);
      }
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
    const request = create(ExportBulkRelationshipsRequestSchema, {
      consistency: consistency._toProto(),
      optionalRelationshipFilter: filter
        ? toProtoRelationshipFilter(filter)
        : undefined,
    });
    let attempt = 0;
    for (;;) {
      let yielded = 0;
      try {
        const stream = this.proto.permissions.exportBulkRelationships(request);
        for await (const resp of stream) {
          for (const protoRel of resp.relationships) {
            yield fromProtoRelationship(protoRel);
            yielded++;
          }
        }
        return;
      } catch (err) {
        if (
          yielded === 0 &&
          (await this.shouldRetryEstablishment(attempt, err))
        ) {
          attempt++;
          continue;
        }
        throw toSpiceDBError(err);
      }
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

    let attempt = 0;
    for (;;) {
      let yielded = 0;
      try {
        const stream = this.proto.watch.watch(req);
        for await (const resp of stream) {
          const changes: WatchChange[] = resp.updates.map((update) => {
            // Server-supplied data: an unrecognized operation
            // (OPERATION_UNSPECIFIED, or a future wire value added after this
            // client shipped) MUST NOT map to a write. Root DESIGN.md, "RULE:
            // A conversion that cannot preserve meaning must fail", clause 2:
            // server-supplied values the client does not recognise MUST NOT
            // raise, and MUST map to the safe, non-permissive default -- never
            // a grant, and never a write. TOUCH is a write, so it can only be
            // the mapping for an explicit OPERATION_TOUCH, never the default:
            // a cache or index mirror consuming this stream would otherwise
            // upsert a relationship that may in fact have been deleted.
            let operation: WatchChange["operation"];
            switch (update.operation) {
              case RelationshipUpdate_Operation.CREATE:
                operation = "create";
                break;
              case RelationshipUpdate_Operation.TOUCH:
                operation = "touch";
                break;
              case RelationshipUpdate_Operation.DELETE:
                operation = "delete";
                break;
              default:
                operation = "unspecified";
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
          yielded++;
        }
        return;
      } catch (err) {
        // Retrying is only safe before any update has been yielded (stream
        // ESTABLISHMENT) — never retry mid-watch, since that would
        // replay/duplicate already-delivered updates.
        if (
          yielded === 0 &&
          (await this.shouldRetryEstablishment(attempt, err))
        ) {
          attempt++;
          continue;
        }
        throw toSpiceDBError(err);
      }
    }
  }

  // ---------------------------------------------------------------------------
  // Retry Logic
  // ---------------------------------------------------------------------------

  /**
   * Decides whether to retry a streaming RPC's ESTABLISHMENT after a
   * transient error, sleeping with the same backoff as `withRetry`.
   *
   * Callers MUST only invoke this when zero items have been yielded from
   * the current stream — retrying after any item has been yielded would
   * replay/duplicate it for the caller. This method only makes the
   * transient/attempt-budget decision; the zero-yielded guard is the
   * caller's responsibility.
   */
  private async shouldRetryEstablishment(
    attempt: number,
    err: unknown,
  ): Promise<boolean> {
    if (!isTransientError(err) || attempt === this.maxRetries) {
      return false;
    }
    const delay = Math.min(100 * 2 ** attempt, 5000);
    await new Promise((resolve) => setTimeout(resolve, delay));
    return true;
  }

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
