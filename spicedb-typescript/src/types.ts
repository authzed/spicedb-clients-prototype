import { SubjectRelationKind, mode } from "./modes.js";
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
import type { Tracing, LookupDebug, LookupPermissionshipFilter, Wildcards, WatchContent, BulkItemErrors } from "./modes.js";
import { DeletePartial } from "./modes.js";
import type { SpiceDBError } from "./errors.js";
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
/**
 * Options accepted by every operation. `signal` and `timeoutMs` bound the whole
 * operation (attempts, backoff, chunks and pages).
 *
 * `headers`, `onHeader` and `onTrailer` are the transport escape hatch: they
 * are forwarded to every RPC the operation makes (each attempt, chunk and
 * page), so `onHeader`/`onTrailer` may be called more than once. This is the
 * supported route to transport-level data, including data that SpiceDB
 * versions before 1.30 send in trailers; it needs no `raw()` client.
 */
export interface CallOptions {
    readonly signal?: AbortSignal;
    readonly timeoutMs?: number;
    /** Extra request headers for this call. They are applied after the client's own, so they can override them. */
    readonly headers?: HeadersInit;
    /** Called with each RPC's response headers. */
    readonly onHeader?: (headers: Headers) => void;
    /** Called with each RPC's response trailers. */
    readonly onTrailer?: (trailers: Headers) => void;
}
export interface CheckOptions extends CallOptions {
    readonly context?: JSONObject;
    readonly tracing?: Tracing;
    /** `checkBulk` only: report per-item failures on `CheckResult.error` instead of failing the call. Defaults to `BulkItemErrors.Fail`. */
    readonly itemErrors?: BulkItemErrors;
}
/** Options for `importRelationships`. */
export interface ImportOptions extends CallOptions {
    /**
     * Relationships per stream message (default 1,000). One import call is one
     * stream and one transaction whatever the batch size; make several calls for
     * several transactions.
     */
    readonly batchSize?: number;
}
export interface Preconditions {
    readonly mustMatch?: readonly RelationshipFilter[];
    readonly mustNotMatch?: readonly RelationshipFilter[];
}
interface DeleteOptionsBase extends CallOptions, Preconditions {
    readonly cursor?: string;
}
/**
 * Options for one bounded `deleteRelationships` request. By default
 * (`DeletePartial.Allowed`) a positive `limit` is required and the server
 * deletes up to it, reporting whether it completed. With
 * `partial: DeletePartial.Disallowed` the delete is all-or-nothing: the server
 * rejects a delete that exceeds the limit instead of deleting part of it, and
 * `limit` may be omitted to use the server's default.
 */
export type DeleteOptions = (DeleteOptionsBase & {
    readonly partial?: DeletePartial.Allowed;
    readonly limit: number;
}) | (DeleteOptionsBase & {
    readonly partial: DeletePartial.Disallowed;
    readonly limit?: number;
});
export interface DeleteAllOptions extends CallOptions, Preconditions {
    readonly pageSize?: number;
    readonly cursor?: string;
    /**
     * Called after every acknowledged batch with the cumulative result so far
     * (count, latest revision and cursor). It runs inline and should be quick;
     * the operation budget keeps running.
     */
    readonly onProgress?: (progress: DeleteResult) => void;
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
    /** Relationships per page; every page repeats it. Default 1,000. */
    readonly pageSize?: number;
    /**
     * Resume after the item whose `afterResultCursor` this is. SpiceDB accepts
     * a cursor only for an identical call: repeat the filter, consistency and
     * page size.
     */
    readonly cursor?: string;
    /** Total relationships to yield, enforced client-side. */
    readonly limit?: number;
}
/**
 * A relationship yielded by `readRelationships`: the relationship's own
 * fields plus paging state, so it can be used anywhere a `Relationship` is.
 */
export interface ReadRelationship extends Relationship {
    /** Resume after this item by passing it as the request `cursor`. */
    readonly afterResultCursor?: string;
    /** The revision the item's page was read at. */
    readonly readAt: ZedToken;
}
/** A relationship yielded by `exportRelationships`, with paging state. */
export interface ExportedRelationship extends Relationship {
    /**
     * The cursor after this item's response batch. Resuming from it skips the
     * rest of that batch, so persist it once the batch's items are handled.
     */
    readonly afterResultCursor?: string;
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
    /** Resume after the batch whose `afterResultCursor` this is. */
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
    /** Resume after this item by passing it as the request `cursor` (repeat the request and page size). */
    readonly afterResultCursor?: string;
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
    /**
     * A per-item failure, set only by `checkBulk` with
     * `itemErrors: BulkItemErrors.Returned`. An errored result has
     * `"unspecified"` permissionship and never grants. Its `inputIndex` is the
     * caller's absolute input index.
     */
    readonly error?: SpiceDBError;
    constructor(readonly permissionship: Permissionship, missingContext: readonly string[], readonly checkedAt: ZedToken, debugTrace?: DebugInformation, expiration?: Date, error?: SpiceDBError) {
        this.missingContext = immutableSnapshot(missingContext);
        this.debugTrace = debugTrace === undefined ? undefined : immutableSnapshot(debugTrace);
        this.expiration = expiration === undefined ? undefined : immutableSnapshot(expiration);
        if (error !== undefined) {
            if (permissionship !== "unspecified")
                throw new InvalidArgumentError("an errored CheckResult must have unspecified permissionship");
            this.error = error;
        }
        Object.freeze(this);
    }
    hasPermission(): boolean { return this.permissionship === "hasPermission"; }
}
/** @internal Deep copy collection ownership at the API boundary. */
export function immutableSnapshot<T>(value: T): T {
    if (value === null || typeof value !== "object" || value instanceof Consistency || value instanceof ZedToken || value instanceof Error)
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
/**
 * Parses SpiceDB's relationship tuple syntax, including the optional caveat
 * and expiration suffixes, in that order:
 *
 *     document:readme#viewer@user:alice
 *     document:readme#viewer@team:eng#member[ip_range:{"cidr":"10.0.0.0/8"}][expiration:2030-01-02T03:04:05Z]
 *
 * A subject relation of `...` means a direct subject. `[name]` is a caveat with
 * no context. Expiration accepts RFC 3339; JavaScript dates keep millisecond
 * precision, so finer digits are truncated. {@link relationshipToString}
 * produces text this function parses.
 */
export function relationshipFromTuple(tuple: string): Relationship {
    const bracket = tuple.indexOf("[");
    const main = bracket < 0 ? tuple : tuple.slice(0, bracket);
    let suffix = bracket < 0 ? "" : tuple.slice(bracket);
    const at = main.split("@");
    if (at.length !== 2)
        throw new InvalidArgumentError(`invalid tuple (requires one "@"): ${tuple}`);
    const resource = at[0].split("#");
    if (resource.length !== 2 || !resource[1])
        throw new InvalidArgumentError(`invalid tuple (requires resource#relation): ${tuple}`);
    const subject = at[1].split("#");
    if (subject.length > 2 || (subject.length === 2 && !subject[1]))
        throw new InvalidArgumentError(`invalid tuple subject: ${tuple}`);
    const object = (text: string): ObjectRef => {
        const i = text.indexOf(":");
        if (i < 1 || i === text.length - 1)
            throw new InvalidArgumentError(`invalid object reference in tuple: ${text}`);
        return { objectType: text.slice(0, i), objectId: text.slice(i + 1) };
    };
    const subjectRelation = subject.length === 2 && subject[1] !== "..." ? subject[1] : undefined;
    let caveat: Caveat | undefined;
    let expiration: Date | undefined;
    const expirationPrefix = "[expiration:";
    if (suffix && (!suffix.startsWith(expirationPrefix) || suffix.startsWith(expirationPrefix + "{"))) {
        const parsed = parseCaveatBracket(suffix);
        caveat = parsed.caveat;
        suffix = parsed.rest;
    }
    if (suffix) {
        if (!suffix.startsWith(expirationPrefix) || !suffix.endsWith("]"))
            throw new InvalidArgumentError(`invalid tuple suffix: ${suffix}`);
        expiration = parseRfc3339(suffix.slice(expirationPrefix.length, -1));
    }
    return immutableSnapshot({
        resource: object(resource[0]), relation: resource[1],
        subject: { object: object(subject[0]), ...(subjectRelation === undefined ? {} : { relation: subjectRelation }) },
        ...(caveat === undefined ? {} : { caveat }), ...(expiration === undefined ? {} : { expiration }),
    });
}
function parseCaveatBracket(text: string): { caveat: Caveat; rest: string } {
    const body = text.slice(1);
    const end = body.search(/[:\]]/);
    if (end <= 0)
        throw new InvalidArgumentError(`invalid tuple caveat: ${text}`);
    const name = body.slice(0, end);
    if (body[end] === "]")
        return { caveat: { name }, rest: body.slice(end + 1) };
    // Scan one JSON object so a "]" inside a context string does not end the bracket.
    const json = body.slice(end + 1);
    let depth = 0, inString = false, i = 0;
    for (; i < json.length; i++) {
        const c = json[i];
        if (inString) {
            if (c === "\\") i++;
            else if (c === "\"") inString = false;
        }
        else if (c === "\"") inString = true;
        else if (c === "{" || c === "[") depth++;
        else if (c === "}" || c === "]") {
            depth--;
            if (depth === 0) break;
        }
    }
    if (json[0] !== "{" || depth !== 0 || json[i + 1] !== "]")
        throw new InvalidArgumentError(`invalid tuple caveat: ${text}`);
    let context: unknown;
    try {
        context = JSON.parse(json.slice(0, i + 1));
    }
    catch (error) {
        throw new InvalidArgumentError(`invalid tuple caveat context: ${text}`, { cause: error });
    }
    return { caveat: { name, context: jsonSnapshot(context, "caveat.context") }, rest: json.slice(i + 2) };
}
function parseRfc3339(text: string): Date {
    const m = /^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2})(?:\.(\d+))?(Z|[+-]\d{2}:\d{2})$/.exec(text);
    if (!m)
        throw new InvalidArgumentError(`invalid tuple expiration: ${text}`);
    const date = new Date(`${m[1]}.${(m[2] ?? "").padEnd(3, "0").slice(0, 3)}${m[3]}`);
    if (!Number.isFinite(date.getTime()))
        throw new InvalidArgumentError(`invalid tuple expiration: ${text}`);
    return date;
}
function sortedJson(value: unknown): string {
    return JSON.stringify(value, (_key, v: unknown) => v !== null && typeof v === "object" && !Array.isArray(v)
        ? Object.fromEntries(Object.entries(v as Record<string, unknown>).sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0)))
        : v);
}
function formatRfc3339(date: Date): string {
    return date.toISOString().replace(/\.(\d*?)0*Z$/, (_m, digits: string) => (digits ? `.${digits}Z` : "Z"));
}
/** Formats an object as `type:id`. */
export function objectRefToString(object: ObjectRef): string { return `${object.objectType}:${object.objectId}`; }
/** Formats a subject as `type:id` or `type:id#relation`. */
export function subjectRefToString(subject: SubjectRef): string {
    return objectRefToString(subject.object) + (subject.relation === undefined ? "" : `#${subject.relation}`);
}
/**
 * Formats a relationship in SpiceDB's tuple syntax, including the caveat and
 * expiration when present. Caveat context keys are sorted; expiration is UTC
 * RFC 3339. {@link relationshipFromTuple} parses the result.
 */
export function relationshipToString(rel: Relationship): string {
    let text = `${objectRefToString(rel.resource)}#${rel.relation}@${subjectRefToString(rel.subject)}`;
    if (rel.caveat !== undefined)
        text += rel.caveat.context !== undefined && Object.keys(rel.caveat.context).length > 0
            ? `[${rel.caveat.name}:${sortedJson(rel.caveat.context)}]`
            : `[${rel.caveat.name}]`;
    if (rel.expiration !== undefined)
        text += `[expiration:${formatRfc3339(rel.expiration)}]`;
    return text;
}
/**
 * Formats a filter in a zed-like form, using `*` for unset fields, e.g.
 * `document:*#viewer@user:alice`, `document:doc-%*#*@*`, or
 * `team:eng#member@user:*#...` for a direct-subject filter.
 */
export function filterToString(filter: RelationshipFilter): string {
    const or = (v: string | undefined) => (v ? v : "*");
    const id = filter.resourceIdPrefix ? `${filter.resourceIdPrefix}%*` : or(filter.resourceId);
    let text = `${filter.resourceType}:${id}#${or(filter.relation)}@${or(filter.subject?.subjectType)}`;
    if (filter.subject) {
        text += `:${or(filter.subject.subjectId)}`;
        const relation = filter.subject.relation;
        if (relation?.kind === SubjectRelationKind.Direct)
            text += "#...";
        else if (relation?.kind === SubjectRelationKind.Named)
            text += `#${relation.relation}`;
    }
    return text;
}
/**
 * Reports whether `filter` matches `rel` with SpiceDB's semantics: resource
 * type, ID or ID prefix, relation, subject type and ID, and the subject
 * relation's Any/Direct/Named distinction. Caveats and expiration are not
 * filterable. Test doubles use this rather than reimplementing the rules.
 */
export function filterMatches(filter: RelationshipFilter, rel: Relationship): boolean {
    if (filter.resourceType !== rel.resource.objectType)
        return false;
    if (filter.resourceId !== undefined && filter.resourceId !== "" && filter.resourceId !== rel.resource.objectId)
        return false;
    if (filter.resourceIdPrefix !== undefined && !rel.resource.objectId.startsWith(filter.resourceIdPrefix))
        return false;
    if (filter.relation !== undefined && filter.relation !== "" && filter.relation !== rel.relation)
        return false;
    const subject = filter.subject;
    if (subject === undefined)
        return true;
    if (subject.subjectType !== rel.subject.object.objectType)
        return false;
    if (subject.subjectId !== undefined && subject.subjectId !== "" && subject.subjectId !== rel.subject.object.objectId)
        return false;
    switch (subject.relation?.kind) {
        case SubjectRelationKind.Direct: return rel.subject.relation === undefined || rel.subject.relation === "";
        case SubjectRelationKind.Named: return rel.subject.relation === subject.relation.relation;
        default: return true;
    }
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
    const partial = mode(options.partial, DeletePartial, DeletePartial.Allowed, "partial");
    const limit = partial === DeletePartial.Disallowed && options.limit === undefined ? 0 : positiveLimit(options.limit as number, "limit");
    return create(DeleteRelationshipsRequestSchema, { relationshipFilter: toProtoRelationshipFilter(filter), optionalPreconditions: toProtoDeletePreconditions(options), optionalLimit: limit, optionalAllowPartialDeletions: partial === DeletePartial.Allowed, optionalCursor: options.cursor ? create(CursorSchema, { token: options.cursor }) : undefined });
}
/** One relationship mutation in a transaction or watch event. */
export interface RelationshipUpdate {
    readonly operation: "create" | "touch" | "delete";
    readonly relationship: Relationship;
}
/** One write precondition. */
export interface Precondition {
    readonly operation: "mustMatch" | "mustNotMatch";
    readonly filter: RelationshipFilter;
}
const UPDATE_OPS = { create: RelationshipUpdate_Operation.CREATE, touch: RelationshipUpdate_Operation.TOUCH, delete: RelationshipUpdate_Operation.DELETE } as const;
const PRECONDITION_OPS = { mustMatch: Precondition_Operation.MUST_MATCH, mustNotMatch: Precondition_Operation.MUST_NOT_MATCH } as const;
/** Formats an update as `TOUCH(document:readme#viewer@user:alice)`. */
export function updateToString(update: RelationshipUpdate): string { return `${update.operation.toUpperCase()}(${relationshipToString(update.relationship)})`; }
/** Formats a precondition as `MUST_MATCH(document:readme#viewer@*)`. */
export function preconditionToString(precondition: Precondition): string {
    return `${precondition.operation === "mustMatch" ? "MUST_MATCH" : "MUST_NOT_MATCH"}(${filterToString(precondition.filter)})`;
}
/** @internal */
export function toProtoUpdate(update: RelationshipUpdate) {
    const operation = UPDATE_OPS[update.operation];
    if (operation === undefined)
        throw new InvalidArgumentError(`unsupported update operation ${String(update.operation)}`);
    return create(RelationshipUpdateSchema, { operation, relationship: toProtoRelationship(update.relationship) });
}
/** @internal */
export function toProtoPreconditionValue(precondition: Precondition): ProtoPrecondition {
    const operation = PRECONDITION_OPS[precondition.operation];
    if (operation === undefined)
        throw new InvalidArgumentError(`unsupported precondition operation ${String(precondition.operation)}`);
    return toProtoPrecondition(operation, precondition.filter);
}
/**
 * Explicitly mutable transaction builder. Each append validates and snapshots
 * caller-owned values. `length` counts updates without copying; `toString()`,
 * `toJSON()` and Node's inspector show its contents.
 */
export class Transaction {
    /** @internal */ private _updates: { native: RelationshipUpdate; proto: ReturnType<typeof toProtoUpdate> }[] = [];
    /** @internal */ private _preconditions: { native: Precondition; proto: ProtoPrecondition }[] = [];
    /** @internal */ private _metadata?: JSONObject;
    create(rel: Relationship): this { return this.update("create", rel); }
    touch(rel: Relationship): this { return this.update("touch", rel); }
    delete(rel: Relationship): this { return this.update("delete", rel); }
    mustMatch(filter: RelationshipFilter): this { return this.precondition("mustMatch", filter); }
    mustNotMatch(filter: RelationshipFilter): this { return this.precondition("mustNotMatch", filter); }
    withMetadata(metadata: JSONObject): this { this._metadata = jsonSnapshot(metadata, "metadata"); return this; }
    /** The number of updates (preconditions excluded). */
    get length(): number { return this._updates.length; }
    /** Immutable snapshots of the updates, in order. */
    get updates(): readonly RelationshipUpdate[] { return Object.freeze(this._updates.map(u => u.native)); }
    /** Immutable snapshots of the preconditions, in order. */
    get preconditions(): readonly Precondition[] { return Object.freeze(this._preconditions.map(p => p.native)); }
    /** Transaction metadata, if set. */
    get metadata(): JSONObject | undefined { return this._metadata; }
    /** `[MUST_NOT_MATCH(organization:acme#admin@*), DELETE(...), TOUCH(...)]` */
    toString(): string { return `[${[...this._preconditions.map(p => preconditionToString(p.native)), ...this._updates.map(u => updateToString(u.native))].join(", ")}]`; }
    toJSON(): { updates: readonly RelationshipUpdate[]; preconditions: readonly Precondition[]; metadata?: JSONObject } {
        return { updates: this.updates, preconditions: this.preconditions, ...(this._metadata === undefined ? {} : { metadata: this._metadata }) };
    }
    /** @internal Node's util.inspect / console.log form. */
    [Symbol.for("nodejs.util.inspect.custom")](): string { return `Transaction ${this.toString()}`; }
    private update(operation: RelationshipUpdate["operation"], rel: Relationship): this {
        const native = immutableSnapshot({ operation, relationship: nativeRelationshipFields(rel) });
        this._updates.push({ native, proto: toProtoUpdate(native) });
        return this;
    }
    private precondition(operation: Precondition["operation"], filter: RelationshipFilter): this {
        const native = immutableSnapshot({ operation, filter });
        this._preconditions.push({ native, proto: toProtoPreconditionValue(native) });
        return this;
    }
    /** @internal */ _build() { return { updates: this._updates.map(u => clone(RelationshipUpdateSchema, u.proto)), optionalPreconditions: this._preconditions.map(p => clone(PreconditionSchema, p.proto)), optionalTransactionMetadata: this._metadata as JsonObject | undefined }; }
}
/** @internal Copies only a relationship's own fields (dropping paging state such as `afterResultCursor`). */
export function nativeRelationshipFields(rel: Relationship): Relationship {
    if (!rel?.resource || !rel.subject?.object)
        throw new InvalidArgumentError("relationship requires resource, relation, and subject");
    return {
        resource: { objectType: rel.resource.objectType, objectId: rel.resource.objectId }, relation: rel.relation,
        subject: { object: { objectType: rel.subject.object.objectType, objectId: rel.subject.object.objectId }, ...(rel.subject.relation === undefined ? {} : { relation: rel.subject.relation }) },
        ...(rel.caveat === undefined ? {} : { caveat: { name: rel.caveat.name, ...(rel.caveat.context === undefined ? {} : { context: rel.caveat.context }) } }),
        ...(rel.expiration === undefined ? {} : { expiration: rel.expiration }),
    };
}
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
export function fromProtoLookupResource(resp: ProtoLookupResourcesResponse, cursor = resp.afterResultCursor?.token): LookupResource {
    return {
        resourceId: resp.resourceObjectId,
        ...(cursor ? { afterResultCursor: cursor } : {}),
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
