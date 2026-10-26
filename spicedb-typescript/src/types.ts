import { SubjectRelationKind } from "./modes.js";
import { create, clone, type JsonObject } from "@bufbuild/protobuf";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import { type Relationship as ProtoRelationship, RelationshipSchema, ObjectReferenceSchema, SubjectReferenceSchema, ContextualizedCaveatSchema, RelationshipUpdateSchema, RelationshipUpdate_Operation, type RelationshipFilter as ProtoRelationshipFilter, RelationshipFilterSchema, SubjectFilterSchema, SubjectFilter_RelationFilterSchema, type Precondition as ProtoPrecondition, PreconditionSchema, Precondition_Operation, DeleteRelationshipsRequestSchema, CursorSchema, type PermissionRelationshipTree as ProtoPermissionRelationshipTree, AlgebraicSubjectSet_Operation, type ReflectionDefinition as ProtoReflectionDefinition, type ReflectionCaveat as ProtoReflectionCaveat, type ReflectionRelationReference as ProtoReflectionRelationReference, type ReflectionSchemaDiff as ProtoReflectionSchemaDiff, type PartialCaveatInfo as ProtoPartialCaveatInfo, type ResolvedSubject as ProtoResolvedSubject, type LookupResourcesResponse as ProtoLookupResourcesResponse, type LookupSubjectsResponse as ProtoLookupSubjectsResponse, LookupPermissionship, type CheckPermissionResponse as ProtoCheckPermissionResponse, type CheckBulkPermissionsResponseItem as ProtoCheckBulkPermissionsResponseItem, } from "@spicedb/proto";
import { Consistency } from "./consistency.js";
import { InvalidArgumentError } from "./errors.js";
export type JSONValue = null | boolean | number | string | JSONObject | readonly JSONValue[];
export interface JSONObject {
    readonly [key: string]: JSONValue;
}
export { ZedToken } from "./token.js";
import { ZedToken, serverToken } from "./token.js";
import type { Tracing, LookupDebug, LookupPermissionshipFilter, Wildcards, WatchContent } from "./modes.js";
export interface ObjectRef {
    readonly objectType: string;
    readonly objectId: string;
}
export interface SubjectRef {
    readonly object: ObjectRef;
    readonly relation?: string;
}
export interface Caveat {
    readonly name: string;
    readonly context?: JSONObject;
}
export interface Relationship {
    readonly resource: ObjectRef;
    readonly relation: string;
    readonly subject: SubjectRef;
    readonly caveat?: Caveat;
    readonly expiration?: Date;
}
export interface CheckRequest {
    readonly resource: ObjectRef;
    readonly permission: string;
    readonly subject: SubjectRef;
    readonly consistency: Consistency;
    readonly context?: JSONObject;
}
export type SubjectRelationFilter = {
    readonly kind: SubjectRelationKind.Any;
} | {
    readonly kind: SubjectRelationKind.Direct;
} | {
    readonly kind: SubjectRelationKind.Named;
    readonly relation: string;
};
export interface SubjectFilter {
    readonly subjectType: string;
    readonly subjectId?: string;
    readonly relation?: SubjectRelationFilter;
}
export interface RelationshipFilter {
    readonly resourceType: string;
    readonly resourceId?: string;
    readonly resourceIdPrefix?: string;
    readonly relation?: string;
    readonly subject?: SubjectFilter;
}
export interface CallOptions {
    readonly signal?: AbortSignal;
    readonly timeoutMs?: number;
}
export interface CheckOptions extends CallOptions {
    readonly context?: JSONObject;
    readonly tracing?: Tracing;
}
export interface Preconditions {
    readonly mustMatch?: readonly RelationshipFilter[];
    readonly mustNotMatch?: readonly RelationshipFilter[];
}
export interface DeleteOptions extends CallOptions, Preconditions {
    readonly limit: number;
    readonly cursor?: string;
}
export interface DeleteAllOptions extends CallOptions, Preconditions {
    readonly pageSize?: number;
    readonly cursor?: string;
}
export interface DeleteResult {
    readonly revision: ZedToken;
    readonly deletedCount: bigint;
    readonly cursor?: string;
    readonly progress: "complete" | "partial" | "unspecified";
}
/** The result of an atomic relationship write. */
export interface WriteResult {
    readonly revision: ZedToken;
}
export interface ReadRelationshipsRequest {
    readonly filter: RelationshipFilter;
    readonly consistency: Consistency;
    readonly pageSize?: number;
    readonly cursor?: string;
}
export interface LookupResourcesRequest {
    /** Client-side filtering; limits count yielded matches. Defaults to All. */
    readonly permissionshipFilter?: LookupPermissionshipFilter;
    readonly resourceType: string;
    readonly permission: string;
    readonly subject: SubjectRef;
    readonly consistency: Consistency;
    readonly context?: JSONObject;
    readonly limit?: number;
    readonly pageSize?: number;
    readonly debug?: LookupDebug;
    readonly cursor?: string;
}
export interface LookupSubjectsRequest {
    readonly resource: ObjectRef;
    readonly permission: string;
    readonly subjectType: string;
    readonly consistency: Consistency;
    readonly subjectRelation?: string;
    readonly context?: JSONObject;
    readonly limit?: number;
    readonly cursor?: string;
    readonly wildcards?: Wildcards;
}
export interface ExpandPermissionTreeRequest {
    readonly resource: ObjectRef;
    readonly permission: string;
    readonly consistency: Consistency;
}
export interface SchemaFilter {
    readonly definitionName?: string;
    readonly caveatName?: string;
    readonly relationName?: string;
    readonly permissionName?: string;
}
export interface ReflectSchemaRequest {
    readonly consistency: Consistency;
    readonly filters?: readonly SchemaFilter[];
}
export interface ComputablePermissionsRequest {
    readonly definitionName: string;
    readonly relationName: string;
    readonly consistency: Consistency;
    readonly definitionNameFilter?: string;
}
export interface DependentRelationsRequest {
    readonly definitionName: string;
    readonly permissionName: string;
    readonly consistency: Consistency;
}
export interface DiffSchemaRequest {
    readonly comparisonSchema: string;
    readonly consistency: Consistency;
}
export interface ExportRelationshipsRequest {
    readonly consistency: Consistency;
    readonly filter?: RelationshipFilter;
    readonly pageSize?: number;
    readonly cursor?: string;
}
export interface WatchRequest {
    readonly objectTypes?: readonly string[];
    readonly startRevision?: ZedToken;
    readonly content?: readonly WatchContent[];
    readonly relationshipFilters?: readonly RelationshipFilter[];
}
/** @experimental */
export interface RoaringLookupResourcesRequest {
    readonly resourceType: string;
    readonly permission: string;
    readonly subject: SubjectRef;
    readonly consistency: Consistency;
}
/** @experimental */
export interface RoaringLookupResourcesResult {
    readonly bitmap: Uint8Array;
    readonly cardinality: bigint;
    readonly revision: ZedToken;
}
export interface WatchChange {
    readonly operation: "create" | "touch" | "delete" | "unspecified";
    readonly relationship: Relationship;
}
export interface WatchEvent {
    readonly updates: readonly WatchChange[];
    readonly changesThrough: ZedToken;
    readonly metadata?: JSONObject;
    readonly schemaUpdated: boolean;
    readonly isCheckpoint: boolean;
}
export type TreeOperation = "unspecified" | "union" | "intersection" | "exclusion";
export type PermissionTreeNode = {
    readonly kind: "intermediate";
    readonly operation: TreeOperation;
    readonly children: readonly PermissionTree[];
} | {
    readonly kind: "leaf";
    readonly subjects: readonly SubjectRef[];
} | {
    readonly kind: "unspecified";
};
export interface PermissionTree {
    readonly expandedObject: ObjectRef;
    readonly expandedRelation: string;
    readonly node: PermissionTreeNode;
}
export interface ExpandResult {
    readonly treeRoot: PermissionTree;
    readonly revision: ZedToken;
}
export interface SchemaCaveatParameter {
    readonly name: string;
    readonly type: string;
    readonly parentCaveatName: string;
}
export interface SchemaCaveat {
    readonly name: string;
    readonly comment: string;
    readonly expression: string;
    readonly parameters: readonly SchemaCaveatParameter[];
}
export interface SchemaTypeReference {
    readonly subjectDefinitionName: string;
    readonly caveatName?: string;
    readonly type: {
        readonly kind: "terminal";
        readonly terminal: boolean;
    } | {
        readonly kind: "relation";
        readonly relation: string;
    } | {
        readonly kind: "wildcard";
        readonly wildcard: boolean;
    } | {
        readonly kind: "unspecified";
    };
}
export interface SchemaRelation {
    readonly name: string;
    readonly comment: string;
    readonly parentDefinitionName: string;
    readonly subjectTypes: readonly SchemaTypeReference[];
}
export interface SchemaPermission {
    readonly name: string;
    readonly comment: string;
    readonly parentDefinitionName: string;
}
export interface SchemaDefinition {
    readonly name: string;
    readonly comment: string;
    readonly relations: readonly SchemaRelation[];
    readonly permissions: readonly SchemaPermission[];
}
export interface ReflectSchemaResult {
    readonly definitions: readonly SchemaDefinition[];
    readonly caveats: readonly SchemaCaveat[];
    readonly revision: ZedToken;
}
export interface RelationReference {
    readonly definitionName: string;
    readonly relationName: string;
    readonly isPermission: boolean;
}
export interface RelationsResult {
    readonly relations: readonly RelationReference[];
    readonly revision: ZedToken;
}
export interface SchemaResult {
    readonly schema: string;
    readonly revision: ZedToken;
}
export interface SchemaDiff {
    readonly kind: string;
    readonly definitionName: string;
    readonly relationName: string;
    readonly permissionName: string;
    readonly caveatName: string;
    readonly definition?: SchemaDefinition;
    readonly relation?: SchemaRelation;
    readonly permission?: SchemaPermission;
    readonly caveat?: SchemaCaveat;
    readonly parameter?: SchemaCaveatParameter;
    readonly changedSubjectType?: SchemaTypeReference;
    readonly previousType?: string;
}
export interface SchemaDiffResult {
    readonly diffs: readonly SchemaDiff[];
    readonly revision: ZedToken;
}
/** @experimental */
export interface RelationshipCountResult {
    readonly stillCalculating: boolean;
    readonly count?: bigint;
    readonly revision?: ZedToken;
}
export type Permissionship = "unspecified" | "hasPermission" | "conditionalPermission" | "noPermission";
export interface PartialCaveatInfo {
    readonly missingRequiredContext: readonly string[];
}
export interface LookupResource {
    readonly resourceId: string;
    readonly permissionship: Permissionship;
    readonly partialCaveat?: PartialCaveatInfo;
    readonly lookedUpAt: ZedToken;
}
export interface ResolvedSubject {
    readonly subjectId: string;
    readonly permissionship: Permissionship;
    readonly partialCaveat?: PartialCaveatInfo;
}
export interface LookupSubject {
    readonly subject: ResolvedSubject;
    readonly excludedSubjects: readonly ResolvedSubject[];
    readonly lookedUpAt: ZedToken;
}
export interface CaveatEvaluation {
    readonly expression: string;
    readonly result: "unspecified" | "unevaluated" | "false" | "true" | "missingContext";
    readonly context?: JSONObject;
    readonly missingContext: readonly string[];
    readonly caveatName: string;
}
export type TraceResolution = {
    readonly kind: "cached";
    readonly cached: boolean;
} | {
    readonly kind: "subproblems";
    readonly traces: readonly CheckDebugTrace[];
} | {
    readonly kind: "unspecified";
};
export interface CheckDebugTrace {
    readonly resource?: ObjectRef;
    readonly permission: string;
    readonly permissionType: "unspecified" | "relation" | "permission";
    readonly subject?: SubjectRef;
    readonly result: Permissionship;
    readonly caveatEvaluation?: CaveatEvaluation;
    /** Exact signed nanoseconds, avoiding floating-point duration loss. */
    readonly durationNs?: bigint;
    readonly resolution: TraceResolution;
    readonly expiration?: Date;
    readonly traceOperationId: string;
    readonly source: string;
}
export interface DebugInformation {
    readonly schemaUsed: string;
    readonly check?: CheckDebugTrace;
}
export class CheckResult {
    readonly missingContext: readonly string[];
    readonly debugTrace?: DebugInformation;
    readonly expiration?: Date;
    constructor(readonly permissionship: Permissionship, missingContext: readonly string[], readonly checkedAt: ZedToken, debugTrace?: DebugInformation, expiration?: Date) {
        this.missingContext = immutableSnapshot(missingContext);
        this.debugTrace = debugTrace === undefined ? undefined : immutableSnapshot(debugTrace);
        this.expiration = expiration === undefined ? undefined : immutableSnapshot(expiration);
        Object.freeze(this);
    }
    hasPermission(): boolean { return this.permissionship === "hasPermission"; }
}
/** @internal Deep copy collection ownership at the API boundary. */
export function immutableSnapshot<T>(value: T): T {
    if (value === null || typeof value !== "object" || value instanceof Consistency || value instanceof ZedToken)
        return value;
    if (value instanceof Date) {
        if (!Number.isFinite(value.getTime()))
            throw new InvalidArgumentError("expiration must be a valid Date");
        const date = new Date(value.getTime());
        for (const name of Object.getOwnPropertyNames(Date.prototype)) {
            if (name.startsWith("set"))
                Object.defineProperty(date, name, { value: () => { throw new TypeError("Date snapshot is immutable"); } });
        }
        return Object.freeze(date) as T;
    }
    if (value instanceof Uint8Array)
        return value.slice() as T;
    if (Array.isArray(value))
        return Object.freeze(value.map(v => immutableSnapshot(v))) as T;
    const copy: Record<string, unknown> = {};
    for (const [k, v] of Object.entries(value))
        Object.defineProperty(copy, k, { value: immutableSnapshot(v), enumerable: true });
    return Object.freeze(copy) as T;
}
/** @internal Validate native JSON before protobuf conversion, reporting the full offending path. */
export function jsonSnapshot(value: unknown, path = "context"): JSONObject {
    const active = new Set<object>();
    function visit(v: unknown, key: string): JSONValue {
        if (v === null || typeof v === "string" || typeof v === "boolean")
            return v;
        if (typeof v === "number" && Number.isFinite(v))
            return v;
        if (typeof v !== "object" || v === null)
            throw new InvalidArgumentError(`${key}: unsupported JSON value`);
        if (active.has(v))
            throw new InvalidArgumentError(`${key}: cyclic JSON value`);
        if (!Array.isArray(v) && Object.getPrototypeOf(v) !== Object.prototype && Object.getPrototypeOf(v) !== null)
            throw new InvalidArgumentError(`${key}: unsupported JSON object`);
        if (Object.getOwnPropertySymbols(v).length)
            throw new InvalidArgumentError(`${key}: symbol JSON key`);
        active.add(v);
        let result: JSONValue;
        if (Array.isArray(v))
            result = Object.freeze(Array.from(v, (item, i) => visit(item, `${key}[${i}]`)));
        else {
            const resultObject: Record<string, JSONValue> = {};
            for (const [k, item] of Object.entries(v))
                Object.defineProperty(resultObject, k, { value: visit(item, `${key}.${k}`), enumerable: true });
            result = Object.freeze(resultObject);
        }
        active.delete(v);
        return result;
    }
    if (value === null || typeof value !== "object" || Array.isArray(value))
        throw new InvalidArgumentError(`${path}: expected a JSON object`);
    return visit(value, path) as JSONObject;
}
/** @internal */
export function mergeCheckContext(defaults?: JSONObject, item?: JSONObject): JsonObject | undefined {
    if (defaults === undefined && item === undefined)
        return undefined;
    return jsonSnapshot({ ...(defaults === undefined ? {} : jsonSnapshot(defaults, "options.context")), ...(item === undefined ? {} : jsonSnapshot(item)) }) as JsonObject;
}
/** @internal */
export function protoSubject(subject: SubjectRef) {
    if (!subject?.object)
        throw new InvalidArgumentError("subject.object is required");
    if (subject.relation !== undefined && !subject.relation)
        throw new InvalidArgumentError("subject.relation must be nonempty when present");
    return create(SubjectReferenceSchema, { object: subject.object, optionalRelation: subject.relation ?? "" });
}
/** @internal */
export function nativeSubject(subject: {
    object?: ObjectRef;
    optionalRelation: string;
}): SubjectRef {
    return immutableSnapshot({ object: subject.object ? { objectType: subject.object.objectType, objectId: subject.object.objectId } : { objectType: "", objectId: "" }, ...(subject.optionalRelation ? { relation: subject.optionalRelation } : {}) });
}
/** @internal */
export function toProtoRelationship(rel: Relationship): ProtoRelationship {
    if (!rel.resource || !rel.subject || !rel.relation)
        throw new InvalidArgumentError("relationship requires resource, relation, and subject");
    if (rel.expiration !== undefined && (!(rel.expiration instanceof Date) || !Number.isFinite(rel.expiration.getTime())))
        throw new InvalidArgumentError("expiration must be a valid Date");
    return create(RelationshipSchema, { resource: rel.resource, relation: rel.relation, subject: protoSubject(rel.subject), optionalCaveat: rel.caveat ? create(ContextualizedCaveatSchema, { caveatName: rel.caveat.name, context: rel.caveat.context === undefined ? undefined : jsonSnapshot(rel.caveat.context, "caveat.context") as JsonObject }) : undefined, optionalExpiresAt: rel.expiration === undefined ? undefined : timestampFromDate(rel.expiration) });
}
/** @internal */
export function fromProtoRelationship(rel: ProtoRelationship): Relationship {
    return immutableSnapshot({ resource: { objectType: rel.resource?.objectType ?? "", objectId: rel.resource?.objectId ?? "" }, relation: rel.relation,
        subject: nativeSubject(rel.subject ?? { optionalRelation: "" }),
        ...(rel.optionalCaveat ? { caveat: { name: rel.optionalCaveat.caveatName, ...(rel.optionalCaveat.context === undefined ? {} : { context: jsonSnapshot(rel.optionalCaveat.context, "caveat.context") }) } } : {}),
        ...(rel.optionalExpiresAt ? { expiration: new Date(Number(rel.optionalExpiresAt.seconds) * 1000 + rel.optionalExpiresAt.nanos / 1e6) } : {}) });
}
export function relationship(resource: string, relation: string, subject: string): Relationship {
    const parse = (value: string): ObjectRef => {
        const i = value.indexOf(":");
        if (i < 1 || i === value.length - 1)
            throw new InvalidArgumentError(`invalid object reference: ${value}`);
        return { objectType: value.slice(0, i), objectId: value.slice(i + 1) };
    };
    const i = subject.indexOf("#");
    return immutableSnapshot({ resource: parse(resource), relation, subject: { object: parse(i < 0 ? subject : subject.slice(0, i)), ...(i < 0 ? {} : { relation: subject.slice(i + 1) }) } });
}
export function relationshipFromTuple(tuple: string, subject: string): Relationship {
    const i = tuple.indexOf("#");
    if (i < 1)
        throw new InvalidArgumentError(`invalid resource tuple: ${tuple}`);
    return relationship(tuple.slice(0, i), tuple.slice(i + 1), subject);
}
export function relationshipToFilter(rel: Relationship): RelationshipFilter {
    return immutableSnapshot({ resourceType: rel.resource.objectType, resourceId: rel.resource.objectId, relation: rel.relation, subject: { subjectType: rel.subject.object.objectType, subjectId: rel.subject.object.objectId, relation: rel.subject.relation === undefined ? { kind: SubjectRelationKind.Direct } : { kind: SubjectRelationKind.Named, relation: rel.subject.relation } } });
}
/** @internal */
export function toProtoRelationshipFilter(filter: RelationshipFilter): ProtoRelationshipFilter {
    if (!filter.resourceType)
        throw new InvalidArgumentError("filter.resourceType is required");
    if (filter.resourceId !== undefined && filter.resourceIdPrefix !== undefined)
        throw new InvalidArgumentError("filter.resourceId and resourceIdPrefix cannot be combined");
    const subject = filter.subject;
    if (subject && !subject.subjectType)
        throw new InvalidArgumentError("filter.subject.subjectType is required");
    const relation = subject?.relation;
    if (relation?.kind === SubjectRelationKind.Named && !relation.relation)
        throw new InvalidArgumentError("filter.subject.relation must be nonempty for named");
    if (relation && !Object.values(SubjectRelationKind).includes(relation.kind))
        throw new InvalidArgumentError("invalid subject relation filter");
    return create(RelationshipFilterSchema, { resourceType: filter.resourceType, optionalResourceId: filter.resourceId ?? "", optionalResourceIdPrefix: filter.resourceIdPrefix ?? "", optionalRelation: filter.relation ?? "",
        optionalSubjectFilter: subject ? create(SubjectFilterSchema, { subjectType: subject.subjectType, optionalSubjectId: subject.subjectId ?? "", optionalRelation: relation === undefined || relation.kind === SubjectRelationKind.Any ? undefined : create(SubjectFilter_RelationFilterSchema, { relation: relation.kind === SubjectRelationKind.Direct ? "" : relation.relation }) }) : undefined });
}
/** @internal */
export function toProtoPrecondition(operation: Precondition_Operation, filter: RelationshipFilter): ProtoPrecondition { return create(PreconditionSchema, { operation, filter: toProtoRelationshipFilter(filter) }); }
/** @internal */
export function toProtoDeletePreconditions(options?: Preconditions): ProtoPrecondition[] { return [...(options?.mustMatch ?? []).map(f => toProtoPrecondition(Precondition_Operation.MUST_MATCH, f)), ...(options?.mustNotMatch ?? []).map(f => toProtoPrecondition(Precondition_Operation.MUST_NOT_MATCH, f))]; }
/** @internal */
export function positiveLimit(value: number, field: string): number {
    if (!Number.isInteger(value) || value <= 0 || value > 0xffffffff)
        throw new InvalidArgumentError(`${field} must be a positive uint32 integer`);
    return value;
}
/** @internal */
export function toProtoDeleteRelationshipsRequest(filter: RelationshipFilter, options: DeleteOptions) {
    if (!options)
        throw new InvalidArgumentError("deleteRelationships options.limit is required");
    return create(DeleteRelationshipsRequestSchema, { relationshipFilter: toProtoRelationshipFilter(filter), optionalPreconditions: toProtoDeletePreconditions(options), optionalLimit: positiveLimit(options.limit, "limit"), optionalAllowPartialDeletions: true, optionalCursor: options.cursor ? create(CursorSchema, { token: options.cursor }) : undefined });
}
/** Explicitly mutable transaction builder. Each append snapshots caller-owned values. */
export class Transaction {
    /** @internal */ private _updates: ReturnType<typeof makeUpdate>[] = [];
    /** @internal */ private _preconditions: ProtoPrecondition[] = [];
    /** @internal */ private _metadata?: JSONObject;
    create(rel: Relationship): this { this._updates.push(makeUpdate(RelationshipUpdate_Operation.CREATE, rel)); return this; }
    touch(rel: Relationship): this { this._updates.push(makeUpdate(RelationshipUpdate_Operation.TOUCH, rel)); return this; }
    delete(rel: Relationship): this { this._updates.push(makeUpdate(RelationshipUpdate_Operation.DELETE, rel)); return this; }
    mustMatch(filter: RelationshipFilter): this { this._preconditions.push(toProtoPrecondition(Precondition_Operation.MUST_MATCH, filter)); return this; }
    mustNotMatch(filter: RelationshipFilter): this { this._preconditions.push(toProtoPrecondition(Precondition_Operation.MUST_NOT_MATCH, filter)); return this; }
    withMetadata(metadata: JSONObject): this { this._metadata = jsonSnapshot(metadata, "metadata"); return this; }
    /** @internal */ _build() { return { updates: this._updates.map(u => clone(RelationshipUpdateSchema, u)), optionalPreconditions: this._preconditions.map(p => clone(PreconditionSchema, p)), optionalTransactionMetadata: this._metadata as JsonObject | undefined }; }
}
function makeUpdate(operation: RelationshipUpdate_Operation, rel: Relationship) { return create(RelationshipUpdateSchema, { operation, relationship: toProtoRelationship(rel) }); }
/** @internal */
export function toTreeOperation(op: AlgebraicSubjectSet_Operation): TreeOperation { return (["unspecified", "union", "intersection", "exclusion"] as const)[op] ?? "unspecified"; }
/** @internal */
export function fromProtoPermissionTree(t?: ProtoPermissionRelationshipTree): PermissionTree {
    return immutableSnapshot({ expandedObject: { objectType: t?.expandedObject?.objectType ?? "", objectId: t?.expandedObject?.objectId ?? "" }, expandedRelation: t?.expandedRelation ?? "", node: t?.treeType.case === "intermediate" ? { kind: "intermediate", operation: toTreeOperation(t.treeType.value.operation), children: t.treeType.value.children.map(fromProtoPermissionTree) } : t?.treeType.case === "leaf" ? { kind: "leaf", subjects: t.treeType.value.subjects.map(nativeSubject) } : { kind: "unspecified" } });
}
/** @internal */
export function fromProtoSchemaDefinition(def: ProtoReflectionDefinition): SchemaDefinition {
    return {
        name: def.name,
        comment: def.comment,
        relations: def.relations.map((rel) => ({
            name: rel.name,
            comment: rel.comment,
            parentDefinitionName: rel.parentDefinitionName,
            subjectTypes: rel.subjectTypes.map(fromProtoTypeReference),
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
export function fromProtoRelationReference(ref: ProtoReflectionRelationReference): RelationReference {
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
function schemaDiffSummary(d: ProtoReflectionSchemaDiff): SchemaDiff {
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
/** @internal */
export function fromProtoTypeReference(value: ProtoReflectionDefinition["relations"][number]["subjectTypes"][number]): SchemaTypeReference {
    return immutableSnapshot({ subjectDefinitionName: value.subjectDefinitionName, ...(value.optionalCaveatName ? { caveatName: value.optionalCaveatName } : {}), type: value.typeref.case === "isTerminalSubject" ? { kind: "terminal", terminal: value.typeref.value } : value.typeref.case === "optionalRelationName" ? { kind: "relation", relation: value.typeref.value } : value.typeref.case === "isPublicWildcard" ? { kind: "wildcard", wildcard: value.typeref.value } : { kind: "unspecified" } });
}
function nativeRelation(value: ProtoReflectionDefinition["relations"][number]): SchemaRelation { return immutableSnapshot({ name: value.name, comment: value.comment, parentDefinitionName: value.parentDefinitionName, subjectTypes: value.subjectTypes.map(fromProtoTypeReference) }); }
function nativeParameter(value: ProtoReflectionCaveat["parameters"][number]): SchemaCaveatParameter { return { name: value.name, type: value.type, parentCaveatName: value.parentCaveatName }; }
/** @internal */
export function fromProtoSchemaDiff(value: ProtoReflectionSchemaDiff): SchemaDiff {
    const summary = schemaDiffSummary(value);
    const d = value.diff;
    switch (d.case) {
        case "definitionAdded":
        case "definitionRemoved":
        case "definitionDocCommentChanged": return immutableSnapshot({ ...summary, definition: fromProtoSchemaDefinition(d.value) });
        case "relationAdded":
        case "relationRemoved":
        case "relationDocCommentChanged": return immutableSnapshot({ ...summary, relation: nativeRelation(d.value) });
        case "relationSubjectTypeAdded":
        case "relationSubjectTypeRemoved": return immutableSnapshot({ ...summary, ...(d.value.relation ? { relation: nativeRelation(d.value.relation) } : {}), ...(d.value.changedSubjectType ? { changedSubjectType: fromProtoTypeReference(d.value.changedSubjectType) } : {}) });
        case "permissionAdded":
        case "permissionRemoved":
        case "permissionDocCommentChanged":
        case "permissionExprChanged": return immutableSnapshot({ ...summary, permission: { name: d.value.name, comment: d.value.comment, parentDefinitionName: d.value.parentDefinitionName } });
        case "caveatAdded":
        case "caveatRemoved":
        case "caveatDocCommentChanged":
        case "caveatExprChanged": return immutableSnapshot({ ...summary, caveat: fromProtoSchemaCaveat(d.value) });
        case "caveatParameterAdded":
        case "caveatParameterRemoved": return immutableSnapshot({ ...summary, parameter: nativeParameter(d.value) });
        case "caveatParameterTypeChanged": return immutableSnapshot({ ...summary, ...(d.value.parameter ? { parameter: nativeParameter(d.value.parameter) } : {}), previousType: d.value.previousType });
        default: return immutableSnapshot(summary);
    }
}
/** @internal */
export function permissionshipFromProto(v: LookupPermissionship): Permissionship {
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
export function partialCaveatFromProto(v?: ProtoPartialCaveatInfo): PartialCaveatInfo | undefined {
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
export function resolvedSubjectFromProto(v?: ProtoResolvedSubject): ResolvedSubject {
    return {
        subjectId: v?.subjectObjectId ?? "",
        permissionship: permissionshipFromProto(v?.permissionship ?? LookupPermissionship.UNSPECIFIED),
        partialCaveat: partialCaveatFromProto(v?.partialCaveatInfo),
    };
}
/**
 * Maps a proto `LookupResourcesResponse` to its native equivalent.
 * @internal
 */
export function fromProtoLookupResource(resp: ProtoLookupResourcesResponse): LookupResource {
    return {
        resourceId: resp.resourceObjectId,
        permissionship: permissionshipFromProto(resp.permissionship),
        partialCaveat: partialCaveatFromProto(resp.partialCaveatInfo),
        lookedUpAt: serverToken(resp.lookedUpAt?.token),
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
export function fromProtoLookupSubject(resp: ProtoLookupSubjectsResponse): LookupSubject {
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
        excludedSubjects = resp.excludedSubjects.map((e) => resolvedSubjectFromProto(e));
    }
    else if (resp.excludedSubjectIds.length > 0) {
        // Fall back to the deprecated excluded_subject_ids field, which carries
        // only IDs (no permissionship/caveat info).
        excludedSubjects = resp.excludedSubjectIds.map((id) => ({
            subjectId: id,
            permissionship: "unspecified" as const,
        }));
    }
    return { subject, excludedSubjects, lookedUpAt: serverToken(resp.lookedUpAt?.token) };
}
/** @internal */
export function checkPermissionshipFromProto(v: number): Permissionship { return (["unspecified", "noPermission", "hasPermission", "conditionalPermission"] as const)[v] ?? "unspecified"; }
type ProtoDebug = NonNullable<ProtoCheckPermissionResponse["debugTrace"]>;
type ProtoTrace = NonNullable<ProtoDebug["check"]>;
/** @internal */
export function fromProtoDebugInformation(value?: ProtoDebug): DebugInformation | undefined { return value === undefined ? undefined : immutableSnapshot({ schemaUsed: value.schemaUsed, ...(value.check ? { check: fromProtoCheckTrace(value.check) } : {}) }); }
function fromProtoCheckTrace(value: ProtoTrace): CheckDebugTrace {
    const caveat = value.caveatEvaluationInfo;
    return immutableSnapshot({
        ...(value.resource ? { resource: { objectType: value.resource.objectType, objectId: value.resource.objectId } } : {}),
        permission: value.permission, permissionType: (["unspecified", "relation", "permission"] as const)[value.permissionType] ?? "unspecified",
        ...(value.subject ? { subject: nativeSubject(value.subject) } : {}), result: checkPermissionshipFromProto(value.result),
        ...(caveat ? { caveatEvaluation: { expression: caveat.expression, result: (["unspecified", "unevaluated", "false", "true", "missingContext"] as const)[caveat.result] ?? "unspecified", ...(caveat.context === undefined ? {} : { context: jsonSnapshot(caveat.context) }), missingContext: [...(caveat.partialCaveatInfo?.missingRequiredContext ?? [])], caveatName: caveat.caveatName } } : {}),
        ...(value.duration ? { durationNs: value.duration.seconds * 1000000000n + BigInt(value.duration.nanos) } : {}),
        resolution: value.resolution.case === "wasCachedResult" ? { kind: "cached", cached: value.resolution.value } : value.resolution.case === "subProblems" ? { kind: "subproblems", traces: value.resolution.value.traces.map(fromProtoCheckTrace) } : { kind: "unspecified" },
        ...(value.optionalExpiresAt ? { expiration: new Date(Number(value.optionalExpiresAt.seconds) * 1000 + value.optionalExpiresAt.nanos / 1e6) } : {}), traceOperationId: value.traceOperationId, source: value.source
    });
}
/** @internal */
export function checkResultFromProto(resp: ProtoCheckPermissionResponse): CheckResult { return new CheckResult(checkPermissionshipFromProto(resp.permissionship), resp.partialCaveatInfo?.missingRequiredContext ?? [], serverToken(resp.checkedAt?.token), fromProtoDebugInformation(resp.debugTrace), resp.optionalExpiresAt ? new Date(Number(resp.optionalExpiresAt.seconds) * 1000 + resp.optionalExpiresAt.nanos / 1e6) : undefined); }
/** @internal */
export function checkResultFromBulkItem(item: ProtoCheckBulkPermissionsResponseItem, revision: ZedToken): CheckResult { return new CheckResult(checkPermissionshipFromProto(item.permissionship), item.partialCaveatInfo?.missingRequiredContext ?? [], revision, fromProtoDebugInformation(item.debugTrace)); }
/** @experimental A resumable permission-set cursor. Every wire field is preserved. */
export interface PermissionSetCursor {
    readonly limit: number;
    readonly token?: ZedToken;
    readonly startingIndex: number;
    readonly completedMembers: boolean;
    readonly startingKey: string;
    readonly cursor: string;
}
/** @experimental */
export interface PermissionSetReference {
    readonly object: ObjectRef;
    readonly permissionOrRelation: string;
}
/** @experimental */
export interface PermissionSetMember {
    readonly object: ObjectRef;
    readonly permissionOrRelation?: string;
}
/** @experimental */
export interface PermissionSetChange {
    readonly revision: ZedToken;
    readonly operation: "added" | "removed" | "unspecified";
    readonly parentSet?: PermissionSetReference;
    readonly child: {
        readonly kind: "set";
        readonly set: PermissionSetReference;
    } | {
        readonly kind: "member";
        readonly member: PermissionSetMember;
    } | {
        readonly kind: "unspecified";
    };
}
/** @experimental */
export interface WatchedPermission {
    readonly resourceType: string;
    readonly permission: string;
    readonly subjectType: string;
    readonly subjectRelation?: string;
}
/** @experimental */
export interface WatchPermissionSetsRequest {
    readonly startRevision?: ZedToken;
}
/** @experimental */
export type PermissionSetEvent = {
    readonly kind: "change";
    readonly change: PermissionSetChange;
} | {
    readonly kind: "completedRevision";
    readonly revision: ZedToken;
} | {
    readonly kind: "lookupRequired";
    readonly revision: ZedToken;
} | {
    readonly kind: "breakingSchemaChange";
    readonly revision: ZedToken;
    readonly affectedPermissions: readonly WatchedPermission[];
} | {
    readonly kind: "unspecified";
};
/** @experimental */
export interface LookupPermissionSetsRequest {
    readonly revision?: ZedToken;
    readonly cursor?: PermissionSetCursor;
    readonly pageSize?: number;
    readonly limit?: number;
}
/** @experimental */
export interface LookupPermissionSet {
    readonly change?: PermissionSetChange;
    readonly cursor?: PermissionSetCursor;
}
/** @experimental */
export interface DownloadPermissionSetsRequest {
    readonly revision?: ZedToken;
}
/** @experimental */
export interface PermissionSetFile {
    readonly name: string;
    readonly url: string;
}
/** @experimental Download URLs are returned without fetching their content. */
export interface DownloadPermissionSetsResult {
    readonly files: readonly PermissionSetFile[];
    readonly timestamp?: Date;
    readonly revision: ZedToken;
}
