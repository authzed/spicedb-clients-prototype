import { Code, ConnectError } from "@connectrpc/connect";
import { Consistency } from "./consistency.js";
import { InvalidArgumentError, SpiceDBError, type SpiceDBErrorOptions } from "./errors.js";
import { DeletePartial, LookupDebug, LookupPermissionshipFilter, Paging, Wildcards, mode } from "./modes.js";
import { OperationAbortError } from "./operation.js";
import { mutationRetry, validRetryPolicy } from "./retry.js";
import { ZedToken } from "./token.js";
import { diffSchemas } from "./schemadiff.js";
import type { SpiceDBClientInterface, SpiceDBProtoClient } from "./client.js";
import {
    CheckResult, Transaction, filterMatches, immutableSnapshot, jsonSnapshot, nativeRelationshipFields, objectRefToString, positiveLimit, preconditionToString, relationshipToFilter,
    relationshipToString, subjectRefToString, toProtoRelationship, toProtoRelationshipFilter, protoObject, protoSubject, requireString, optionalString, requireObject, requireObjectArray, requireIterable,
    type CallOptions, type CheckOptions, type CheckRequest, type DeleteAllOptions, type DeleteOptions, type DeleteResult, type ExportedRelationship,
    type ExportRelationshipsRequest, type ImportOptions, type ObjectRef, type Permissionship, type Precondition, type ReadRelationship,
    type ReadRelationshipsRequest, type ReflectSchemaRequest, type ReflectSchemaResult, type Relationship, type RelationshipFilter, type SchemaResult,
    type SubjectRef, type WriteResult, type LookupResourcesRequest, type LookupResource, type LookupSubjectsRequest, type LookupSubject,
    type ExpandPermissionTreeRequest, type ExpandResult, type ComputablePermissionsRequest, type DependentRelationsRequest, type RelationsResult,
    type DiffSchemaRequest, type SchemaDiff, type SchemaDiffResult, type WatchRequest, type WatchEvent, type RelationshipCountResult, type WatchPermissionSetsRequest,
    type PermissionSetEvent, type LookupPermissionSetsRequest, type LookupPermissionSet, type DownloadPermissionSetsRequest,
    type DownloadPermissionSetsResult, type RoaringLookupResourcesRequest, type RoaringLookupResourcesResult,
} from "./types.js";

/**
 * The operations {@link InMemorySpiceDB} records, named like the client's
 * methods. Those after `lookupSubjects` (except `diffSchema`) have no in-memory semantics: they
 * are recorded, consult the fault hook, and fail with `Code.Unimplemented`.
 */
export type InMemoryOperation = "write" | "readRelationships" | "exportRelationships" | "importRelationships" | "deleteRelationships"
    | "deleteAllRelationshipsNonAtomic" | "readSchema" | "writeSchema" | "reflectSchema" | "check" | "checkBulk"
    | "lookupResources" | "lookupSubjects" | "expandPermissionTree" | "computablePermissions" | "dependentRelations" | "diffSchema"
    | "watch" | "experimentalRegisterRelationshipCounter" | "experimentalCountRelationships" | "experimentalUnregisterRelationshipCounter"
    | "experimentalCountRelationshipsByFilter" | "experimentalWatchPermissionSets" | "experimentalLookupPermissionSets"
    | "experimentalDownloadPermissionSets" | "experimentalRoaringLookupResources";

/** The arguments {@link InMemorySpiceDB} records for each operation: the call's request and its options as given. */
export interface InMemoryCallArguments {
    readonly write: { readonly request: Transaction; readonly options?: CallOptions };
    readonly readRelationships: { readonly request: ReadRelationshipsRequest; readonly options?: CallOptions };
    readonly exportRelationships: { readonly request: ExportRelationshipsRequest; readonly options?: CallOptions };
    /** The request is the caller's relationship iterable, not a copy of its items. */
    readonly importRelationships: { readonly request: Iterable<Relationship> | AsyncIterable<Relationship>; readonly options?: ImportOptions };
    readonly deleteRelationships: { readonly request: RelationshipFilter; readonly options: DeleteOptions };
    readonly deleteAllRelationshipsNonAtomic: { readonly request: RelationshipFilter; readonly options?: DeleteAllOptions };
    readonly readSchema: { readonly request: undefined; readonly options?: CallOptions };
    readonly writeSchema: { readonly request: string; readonly options?: CallOptions };
    readonly reflectSchema: { readonly request: ReflectSchemaRequest; readonly options?: CallOptions };
    readonly check: { readonly request: CheckRequest; readonly options?: CheckOptions };
    readonly checkBulk: { readonly request: readonly CheckRequest[]; readonly options?: CheckOptions };
    readonly lookupResources: { readonly request: LookupResourcesRequest; readonly options?: CallOptions };
    readonly lookupSubjects: { readonly request: LookupSubjectsRequest; readonly options?: CallOptions };
    readonly expandPermissionTree: { readonly request: ExpandPermissionTreeRequest; readonly options?: CallOptions };
    readonly computablePermissions: { readonly request: ComputablePermissionsRequest; readonly options?: CallOptions };
    readonly dependentRelations: { readonly request: DependentRelationsRequest; readonly options?: CallOptions };
    readonly diffSchema: { readonly request: DiffSchemaRequest; readonly options?: CallOptions };
    readonly watch: { readonly request: WatchRequest | undefined; readonly options?: CallOptions };
    /** The request is the counter's name and filter. */
    readonly experimentalRegisterRelationshipCounter: { readonly request: { readonly name: string; readonly filter: RelationshipFilter }; readonly options?: CallOptions };
    /** The request is the counter's name. */
    readonly experimentalCountRelationships: { readonly request: string; readonly options?: CallOptions };
    /** The request is the counter's name. */
    readonly experimentalUnregisterRelationshipCounter: { readonly request: string; readonly options?: CallOptions };
    readonly experimentalCountRelationshipsByFilter: { readonly request: RelationshipFilter; readonly options?: CallOptions };
    readonly experimentalWatchPermissionSets: { readonly request: WatchPermissionSetsRequest | undefined; readonly options?: CallOptions };
    readonly experimentalLookupPermissionSets: { readonly request: LookupPermissionSetsRequest; readonly options?: CallOptions };
    readonly experimentalDownloadPermissionSets: { readonly request: DownloadPermissionSetsRequest | undefined; readonly options?: CallOptions };
    readonly experimentalRoaringLookupResources: { readonly request: RoaringLookupResourcesRequest; readonly options?: CallOptions };
}

/**
 * One call {@link InMemorySpiceDB} recorded, discriminated by `operation`:
 * `index` counts calls of that operation from 0, and `request`/`options` are
 * what the caller passed (options are a frozen shallow copy).
 */
export type RecordedCall<K extends InMemoryOperation = InMemoryOperation> = {
    [P in K]: { readonly operation: P; readonly index: number } & InMemoryCallArguments[P];
}[K];

/** Where in an operation an {@link InMemorySpiceDB} fault hook is consulted. */
export enum FaultStage {
    /** Before the operation does anything; a stream fails before its first item. */
    Call = "call",
    /**
     * Once import batch `Fault.item` (0-based) is full, or the input ends,
     * before it is sent; `Fault.batch` holds its relationships. Batches have
     * the call's `batchSize` (default 1,000).
     */
    ImportBatch = "importBatch",
    /** After every import batch, before the import commits. */
    ImportCommit = "importCommit",
    /**
     * Before a read, export or lookup yields item `Fault.item` (0-based). On
     * `readRelationships`, `exportRelationships` and `lookupResources` the
     * injected error carries `resumeCursor` as the real client's does (the
     * last delivered item's `afterResultCursor`, else the request's cursor),
     * so resume logic can be tested on the double.
     */
    Item = "item",
}

/** A point at which {@link InMemorySpiceDB}'s fault hook is consulted. */
export interface Fault {
    readonly call: RecordedCall;
    readonly stage: FaultStage;
    /** The batch index for `FaultStage.ImportBatch`, the item index for `FaultStage.Item`; 0 otherwise. */
    readonly item: number;
    /** The relationships in the batch at `FaultStage.ImportBatch`; `undefined` otherwise. */
    readonly batch?: readonly Relationship[];
}

/** Whether {@link InMemorySpiceDB.readSchema} reports a revision; see {@link InMemorySpiceDB.setReadSchemaRevision}. */
export enum SchemaRevision {
    /** Report the store's revision. The default. */
    Included = "included",
    /**
     * Report none (an empty `ZedToken`), as some hosted SpiceDB deployments
     * do, so code that falls back to another revision source can be tested.
     */
    Omitted = "omitted",
}

/**
 * Consulted at every {@link Fault} point; returning an error makes the
 * operation fail with it there (a stream throws it from iteration). Build
 * errors with {@link InMemorySpiceDB.serverError} to match the real client's.
 */
export type FaultHook = (fault: Fault) => Error | undefined | void;

/** Identifies a check whose result {@link InMemorySpiceDB.setCheckResult} registers. */
export interface CheckRegistration {
    readonly resource: ObjectRef;
    readonly permission: string;
    readonly subject: SubjectRef;
}

/**
 * Decides a check {@link InMemorySpiceDB} has no registered result for. It
 * receives the check with the call's default context (`CheckOptions.context`)
 * merged in, the check's own keys winning; for a lookup, the check of one
 * candidate object with the lookup's consistency and context. It returns a
 * permissionship, or `undefined` to decline and fall through to the stored
 * relationships. It never fails a call: inject failures with the fault hook.
 * Returning `"unspecified"` is a test bug, rejected with `InvalidArgumentError`:
 * SpiceDB never answers with it.
 */
export type CheckResolver = (check: CheckRequest) => Permissionship | undefined;

/**
 * An in-memory test double for {@link SpiceDBClient}. It implements
 * {@link SpiceDBClientInterface}, the client's full method set, so that type
 * (or a caller-defined `Pick<SpiceDBClient, "write" | "check">`) accepts both.
 * `write`, `readRelationships`, `deleteRelationships`,
 * `deleteAllRelationshipsNonAtomic`, `importRelationships`,
 * `exportRelationships`, `readSchema`, `writeSchema`, `reflectSchema`, `check`,
 * `checkBulk`, `lookupResources`, `lookupSubjects`, `diffSchema` (a name-level diff
 * of the stored schema text) and `close` work in memory; `ready` does nothing.
 * The other operations (expand, computable and dependent relations, watch and the
 * experimental methods) are
 * recorded, consult the fault hook, and then fail with `Code.Unimplemented` in
 * the client's error shape (a stream fails from iteration); `raw()` throws.
 *
 * It applies SpiceDB's relationship semantics: atomic transactions,
 * preconditions (`FailedPreconditionError`), CREATE conflicts and duplicate
 * imports (`AlreadyExistsError`), empty writes (preconditions checked, current
 * revision returned), filter matching via {@link filterMatches}, bounded and
 * all-or-nothing deletes, and expired relationships disappearing from reads.
 * It does not evaluate a schema. A check resolves in a fixed order: the result
 * registered for that exact check ({@link InMemorySpiceDB.setCheckResult});
 * then the {@link CheckResolver} set with
 * {@link InMemorySpiceDB.setCheckResolver} ({@link InMemorySpiceDB.grantAll}
 * grants everything); then the stored relationships, where a live
 * relationship on the resource whose relation is the permission's name and
 * whose subject matches exactly or by wildcard grants it (`conditionalPermission`
 * when every such relationship is caveated); anything else is `noPermission`.
 * A subject relation of `...` means a direct subject throughout.
 * `lookupResources` and `lookupSubjects` enumerate the objects of the
 * requested type that appear in the stored relationships (as resource or
 * subject), in order of first appearance among the stored relationships (in
 * the identity order reads yield), and yield those the check resolution
 * grants; conditional grants are `conditionalPermission` with partial-caveat
 * info naming no missing context (caveats are not evaluated). They honor the
 * permissionship filter, wildcard option, `limit` and `cursor` (the decimal
 * count of granted objects before the resume point, including those the
 * permissionship filter drops, as {@link InMemorySpiceDB.cursorAfter}
 * builds); a stored wildcard subject is returned as `*`. `writeSchema` stores the text
 * without compiling it, and `reflectSchema` returns the result registered
 * with {@link InMemorySpiceDB.setReflectSchemaResult}.
 *
 * Errors have the same classes, codes and message shape as the real client's
 * for the same failure: a failure SpiceDB reports carries the server message
 * verbatim on `serverMessage` and Connect's `[code]` prefix on `message`;
 * local validation is an `InvalidArgumentError` without `serverMessage`.
 *
 * Options are plain values and are honored where they change results: read,
 * export and import cursors, limits and batch sizes, the export filter,
 * `retryPolicy` validation (non-idempotent mutations reject the opt-in), abort
 * signals, and `onHeader`/`onTrailer` (called with the metadata registered by
 * {@link InMemorySpiceDB.setResponseMetadata}). Read cursors are positions in
 * the store's identity order; {@link InMemorySpiceDB.cursorAfter} builds one.
 *
 * Every call is recorded ({@link InMemorySpiceDB.calls},
 * {@link InMemorySpiceDB.callsOf}) with its request and options, so tests can
 * assert on what was sent without a strict mock. Faults can be injected
 * ({@link InMemorySpiceDB.setFaultHook}, {@link InMemorySpiceDB.failNext})
 * before any operation, before each import batch, before an import commits,
 * and before each read or export item; a failed write or import applies
 * nothing.
 */
export class InMemorySpiceDB implements SpiceDBClientInterface {
    #relationships = new Map<string, Relationship>();
    #checks = new Map<string, Permissionship | SpiceDBError>();
    #resolver?: CheckResolver;
    #revision = 0;
    #closed = false;
    #schema?: string;
    #reflection?: Omit<ReflectSchemaResult, "revision">;
    #header?: HeadersInit;
    #trailer?: HeadersInit;
    #calls: RecordedCall[] = [];
    #counts = new Map<InMemoryOperation, number>();
    #hook?: FaultHook;
    #omitSchemaRevision = false;

    /**
     * Builds the error the real client raises when SpiceDB answers with `code`
     * and `message`: the subclass for `code`, Connect's `[code]` prefix on
     * `message`, the server message verbatim on `serverMessage`, and the
     * `ConnectError` as `cause`. Use it in fault hooks.
     */
    static serverError(code: Code, message: string, options?: Omit<SpiceDBErrorOptions, "code" | "cause" | "serverMessage">): SpiceDBError {
        return serverError(code, message, options);
    }

    /** A {@link CheckResolver} that grants every check: `db.setCheckResolver(InMemorySpiceDB.grantAll)`. */
    static readonly grantAll: CheckResolver = () => "hasPermission";

    /**
     * The cursor that resumes a read or export after the first `n` matching
     * relationships (in identity order), or a lookup after the first `n`
     * granted objects (in order of first appearance): the `afterResultCursor` the double puts on
     * the `n`-th item. The format, the decimal count `n`, is stable for tests.
     */
    static cursorAfter(n: number): string {
        if (!Number.isSafeInteger(n) || n < 0)
            throw new InvalidArgumentError("cursorAfter requires a non-negative integer");
        return String(n);
    }

    constructor(initial: Iterable<Relationship> = []) {
        for (const rel of initial)
            this.#relationships.set(identity(rel), snapshot(rel));
    }

    /** Every live relationship, in identity order. */
    get relationships(): readonly Relationship[] { return Object.freeze([...this.#live()].map(([, rel]) => rel)); }

    /**
     * Registers the result (or error) a check of `registration` returns.
     * `"unspecified"` is rejected (`InvalidArgumentError`): SpiceDB never
     * answers a check with it, and the real client fails such a response
     * with `Internal`.
     */
    setCheckResult(registration: CheckRegistration, result: Permissionship | SpiceDBError): void {
        if (result === "unspecified")
            throw new InvalidArgumentError("setCheckResult: \"unspecified\" is not a check result SpiceDB returns; register a specified permissionship or an error");
        this.#checks.set(checkKey(registration), result);
    }

    /**
     * Installs the resolver consulted for checks (and lookup candidates) with
     * no registered result, before the stored relationships; `undefined`
     * removes it.
     */
    setCheckResolver(resolver: CheckResolver | undefined): void { this.#resolver = resolver; }

    /** Registers the definitions and caveats `reflectSchema` returns (stamped with the current revision). */
    setReflectSchemaResult(result: Omit<ReflectSchemaResult, "revision">): void {
        this.#reflection = immutableSnapshot({ definitions: result.definitions, caveats: result.caveats });
    }

    /** Selects whether `readSchema` reports a revision ({@link SchemaRevision.Included} by default). */
    setReadSchemaRevision(mode: SchemaRevision): void {
        if (mode !== SchemaRevision.Included && mode !== SchemaRevision.Omitted)
            throw new InvalidArgumentError(`unknown schema revision mode ${String(mode)}`);
        this.#omitSchemaRevision = mode === SchemaRevision.Omitted;
    }

    /** Every recorded call, in order. */
    get calls(): readonly RecordedCall[] { return Object.freeze([...this.#calls]); }

    /** The recorded calls of one operation, in order. */
    callsOf<K extends InMemoryOperation>(operation: K): readonly RecordedCall<K>[] {
        return Object.freeze(this.#calls.filter(call => call.operation === operation) as unknown as RecordedCall<K>[]);
    }

    /**
     * Installs `hook`, consulted at every {@link Fault} point; an error it
     * returns fails the operation there. Pass `undefined` to remove it.
     */
    setFaultHook(hook: FaultHook | undefined): void { this.#hook = hook; }

    /** Makes the next call of `operation` fail with `error` before doing anything; later calls succeed. Replaces any fault hook. */
    failNext(operation: InMemoryOperation, error: Error): void {
        let failed = false;
        this.#hook = fault => {
            if (failed || fault.stage !== FaultStage.Call || fault.call.operation !== operation)
                return undefined;
            failed = true;
            return error;
        };
    }

    /** Sets the headers and trailers every operation reports to its `onHeader`/`onTrailer` callbacks. */
    setResponseMetadata(header?: HeadersInit, trailer?: HeadersInit): void {
        this.#header = header === undefined ? undefined : new Headers(header);
        this.#trailer = trailer === undefined ? undefined : new Headers(trailer);
    }

    /** Applies the transaction atomically; an empty transaction checks its preconditions and returns the current revision, as SpiceDB does. */
    async write(transaction: Transaction, options?: CallOptions): Promise<WriteResult> {
        this.#start("write", transaction, options);
        if (!(transaction instanceof Transaction))
            throw new InvalidArgumentError("write requires a Transaction");
        mutationRetry(options?.retryPolicy, undefined, transaction.isIdempotent(), "write");
        this.#begin(options);
        const next = new Map(this.#relationships);
        this.#preconditions(next, transaction.preconditions);
        if (transaction.length === 0)
            return immutableSnapshot({ revision: new ZedToken(String(this.#revision)) });
        for (const update of transaction.updates) {
            const key = identity(update.relationship);
            if (update.operation === "create" && this.#isLive(next.get(key)))
                throw serverError(Code.AlreadyExists, `could not CREATE relationship \`${relationshipToString(update.relationship)}\`, as it already existed`, { reason: "ERROR_REASON_ATTEMPT_TO_RECREATE_RELATIONSHIP", reasonDomain: "authzed.com" });
            if (update.operation === "delete")
                next.delete(key);
            else
                next.set(key, update.relationship);
        }
        this.#relationships = next;
        return immutableSnapshot({ revision: this.#bump() });
    }

    /** Yields matching relationships in identity order, honoring `cursor` and `limit`; each carries its `afterResultCursor`. */
    readRelationships(request: ReadRelationshipsRequest, options?: CallOptions): AsyncIterableIterator<ReadRelationship> {
        const call = this.#record("readRelationships", request, options);
        requireObject(request, "readRelationships request");
        const fault = this.#faultAt(call, FaultStage.Call, 0);
        if (fault !== undefined)
            return failedStream(withResumeCursor(fault, request.cursor));
        requireConsistency(request.consistency);
        toProtoRelationshipFilter(request.filter);
        const limit = request.limit === undefined ? undefined : positiveLimit(request.limit, "limit");
        this.#begin(options);
        const readAt = new ZedToken(String(this.#revision));
        return streamOf(() => this.#page(rel => filterMatches(request.filter, rel), request.cursor, limit)
            .map(([rel, afterResultCursor]) => immutableSnapshot({ ...rel, afterResultCursor, readAt }) as ReadRelationship), index => this.#faultAt(call, FaultStage.Item, index), { cursor: request.cursor });
    }

    /** Yields every relationship (or those matching `filter`) after `cursor`; each carries its `afterResultCursor`. */
    exportRelationships(request: ExportRelationshipsRequest, options?: CallOptions): AsyncIterableIterator<ExportedRelationship> {
        const call = this.#record("exportRelationships", request, options);
        requireObject(request, "exportRelationships request");
        const fault = this.#faultAt(call, FaultStage.Call, 0);
        if (fault !== undefined)
            return failedStream(withResumeCursor(fault, request.cursor));
        requireConsistency(request.consistency);
        if (request.filter !== undefined)
            toProtoRelationshipFilter(request.filter);
        positiveLimit(request.pageSize ?? 1000, "pageSize");
        this.#begin(options);
        const filter = request.filter;
        return streamOf(() => this.#page(rel => filter === undefined || filterMatches(filter, rel), request.cursor, undefined)
            .map(([rel, afterResultCursor]) => immutableSnapshot({ ...rel, afterResultCursor }) as ExportedRelationship), index => this.#faultAt(call, FaultStage.Item, index), { cursor: request.cursor });
    }

    /** Adds every relationship in one transaction, as SpiceDB does: if any already exists (or repeats), nothing is added (`AlreadyExistsError`). */
    async importRelationships(relationships: Iterable<Relationship> | AsyncIterable<Relationship>, options?: ImportOptions): Promise<bigint> {
        const call = this.#start("importRelationships", relationships, options);
        requireIterable(relationships, "importRelationships relationships");
        mutationRetry(options?.retryPolicy, undefined, false, "importRelationships");
        const batchSize = positiveLimit(options?.batchSize ?? 1000, "batchSize");
        this.#begin(options);
        const batch: Relationship[] = [];
        let pending: Relationship[] = [];
        const send = () => {
            this.#fault(call, FaultStage.ImportBatch, batch.length / batchSize, pending);
            batch.push(...pending);
            pending = [];
        };
        for await (const rel of relationships) {
            toProtoRelationship(rel);
            pending.push(snapshot(rel));
            if (pending.length === batchSize)
                send();
        }
        if (pending.length > 0)
            send();
        this.#fault(call, FaultStage.ImportCommit, 0);
        const next = new Map(this.#relationships);
        for (const rel of batch) {
            const key = identity(rel);
            if (this.#isLive(next.get(key)))
                throw serverError(Code.AlreadyExists, `found duplicate relationship \`${relationshipToString(rel)}\` while importing`);
            next.set(key, rel);
        }
        this.#relationships = next;
        this.#bump();
        return BigInt(batch.length);
    }

    async deleteRelationships(filter: RelationshipFilter, options: DeleteOptions): Promise<DeleteResult> {
        this.#start("deleteRelationships", filter, options);
        mutationRetry(options?.retryPolicy, undefined, false, "deleteRelationships");
        return this.#delete(filter, options);
    }

    async deleteAllRelationshipsNonAtomic(filter: RelationshipFilter, options?: DeleteAllOptions): Promise<DeleteResult> {
        this.#start("deleteAllRelationshipsNonAtomic", filter, options);
        mutationRetry(options?.retryPolicy, undefined, false, "deleteAllRelationshipsNonAtomic");
        const pageSize = positiveLimit(options?.pageSize ?? 1000, "pageSize");
        let total = 0n;
        for (;;) {
            const result = await this.#delete(filter, { ...options, retryPolicy: undefined, limit: pageSize });
            total += result.deletedCount;
            const progress: DeleteResult = immutableSnapshot({ ...result, deletedCount: total });
            options?.onProgress?.(progress);
            if (progress.progress === "complete")
                return progress;
        }
    }

    async #delete(filter: RelationshipFilter, options: DeleteOptions): Promise<DeleteResult> {
        toProtoRelationshipFilter(filter);
        if (!options)
            throw new InvalidArgumentError("deleteRelationships options.limit is required");
        const partial = mode(options?.partial, DeletePartial, DeletePartial.Allowed, "partial");
        const limit = partial === DeletePartial.Disallowed && options.limit === undefined ? undefined : positiveLimit(options.limit as number, "limit");
        this.#begin(options);
        const next = new Map(this.#relationships);
        this.#preconditions(next, [...(options?.mustMatch ?? []).map(filter => ({ operation: "mustMatch" as const, filter })), ...(options?.mustNotMatch ?? []).map(filter => ({ operation: "mustNotMatch" as const, filter }))]);
        const matches = [...this.#live(next)].filter(([, rel]) => filterMatches(filter, rel)).map(([key]) => key);
        if (partial === DeletePartial.Disallowed && limit !== undefined && matches.length > limit)
            throw serverError(Code.InvalidArgument, `found more than ${limit} relationships to be deleted and partial deletion was not requested`);
        const deleted = limit === undefined ? matches : matches.slice(0, limit);
        for (const key of deleted)
            next.delete(key);
        this.#relationships = next;
        return immutableSnapshot({ revision: this.#bump(), deletedCount: BigInt(deleted.length), progress: deleted.length < matches.length ? "partial" : "complete" });
    }

    /**
     * The schema last written, or `NotFoundError` as SpiceDB reports before any
     * schema exists. Its revision is empty under {@link SchemaRevision.Omitted}.
     */
    async readSchema(options?: CallOptions): Promise<SchemaResult> {
        this.#start("readSchema", undefined, options);
        this.#begin(options);
        if (this.#schema === undefined)
            throw serverError(Code.NotFound, "No schema has been defined; please call WriteSchema to start");
        return immutableSnapshot({ schema: this.#schema, revision: new ZedToken(this.#omitSchemaRevision ? "" : String(this.#revision)) });
    }

    /** Stores the schema text; it is not compiled or validated. */
    async writeSchema(schema: string, options?: CallOptions): Promise<ZedToken> {
        this.#start("writeSchema", schema, options);
        requireString(schema, "schema");
        mutationRetry(options?.retryPolicy, undefined, true, "writeSchema");
        this.#begin(options);
        this.#schema = schema;
        return this.#bump();
    }

    /** The result registered with {@link InMemorySpiceDB.setReflectSchemaResult}, or `NotFoundError` without one. */
    async reflectSchema(request: ReflectSchemaRequest, options?: CallOptions): Promise<ReflectSchemaResult> {
        this.#start("reflectSchema", request, options);
        requireObject(request, "reflectSchema request");
        if (request.filters != null)
            requireObjectArray(request.filters, "reflectSchema filters", index => `filters[${index}]`);
        requireConsistency(request.consistency);
        this.#begin(options);
        if (this.#reflection === undefined)
            throw serverError(Code.NotFound, "No schema has been defined; please call WriteSchema to start");
        return immutableSnapshot({ ...this.#reflection, revision: new ZedToken(String(this.#revision)) });
    }

    async check(request: CheckRequest, options?: CheckOptions): Promise<CheckResult> {
        this.#start("check", request, options);
        requireObject(request, "check request");
        requireCheckShape(request, "");
        this.#begin(options);
        return this.#check(request, options?.context);
    }

    async checkBulk(requests: readonly CheckRequest[], options?: CheckOptions): Promise<readonly CheckResult[]> {
        this.#start("checkBulk", requests, options);
        requireObjectArray(requests, "checkBulk requests", index => `check item ${index}: request`);
        requests.forEach((request, index) => requireCheckShape(request, `check item ${index}: `));
        this.#begin(options);
        const results: CheckResult[] = [];
        for (const [index, request] of requests.entries()) {
            try {
                results.push(this.#check(request, options?.context));
            }
            catch (error) {
                if (!(error instanceof SpiceDBError) || options?.itemErrors !== "returned")
                    throw error;
                Object.defineProperty(error, "inputIndex", { value: index, enumerable: true, configurable: true });
                results.push(new CheckResult("unspecified", [], new ZedToken(String(this.#revision)), undefined, undefined, error));
            }
        }
        return Object.freeze(results);
    }

    close(): void { this.#closed = true; }

    /** Does nothing: the double is always ready. */
    async ready(options?: CallOptions): Promise<void> { void options; }

    /** Throws `Code.Unimplemented`: the double has no protocol client. */
    raw(): SpiceDBProtoClient {
        throw SpiceDBError.fromCode(Code.Unimplemented, "InMemorySpiceDB has no protocol client");
    }

    /**
     * Yields the objects of `resourceType` known to the store (as resource or
     * subject of a live relationship) that the check resolution grants
     * `subject`, in order of first appearance, honoring the permissionship filter, `limit` and
     * `cursor`; each carries its `afterResultCursor`.
     */
    lookupResources(request: LookupResourcesRequest, options?: CallOptions): AsyncIterableIterator<LookupResource> {
        const call = this.#record("lookupResources", request, options);
        requireObject(request, "lookupResources request");
        const fault = this.#faultAt(call, FaultStage.Call, 0);
        if (fault !== undefined)
            return failedStream(withResumeCursor(fault, request.cursor));
        const filter = mode(request.permissionshipFilter, LookupPermissionshipFilter, LookupPermissionshipFilter.All, "permissionshipFilter");
        const single = mode(request.paging, Paging, Paging.Automatic, "paging") === Paging.SingleRequest;
        mode(request.debug, LookupDebug, LookupDebug.Disabled, "debug");
        requireString(request.resourceType, "resourceType");
        requireString(request.permission, "permission");
        protoSubject(request.subject);
        if (request.cursor !== undefined)
            requireString(request.cursor, "cursor");
        const limit = request.limit === undefined ? undefined : positiveLimit(request.limit, "limit");
        if (!single)
            positiveLimit(request.pageSize ?? 1000, "pageSize");
        requireConsistency(request.consistency);
        if (request.context !== undefined)
            jsonSnapshot(request.context);
        this.#begin(options);
        const lookedUpAt = new ZedToken(String(this.#revision));
        const context = request.context === undefined ? {} : { context: request.context };
        return streamOf(() => this.#lookup(request.resourceType, request.cursor, limit,
            id => this.#resolve({ resource: { objectType: request.resourceType, objectId: id }, permission: request.permission, subject: request.subject, consistency: request.consistency, ...context }),
            permissionship => filter === LookupPermissionshipFilter.All || permissionship === "hasPermission")
            .map(([resourceId, permissionship, afterResultCursor]) => immutableSnapshot({ resourceId, afterResultCursor, permissionship, ...partialCaveat(permissionship), lookedUpAt }) as LookupResource),
        index => this.#faultAt(call, FaultStage.Item, index), { cursor: request.cursor });
    }

    /**
     * Yields the objects of `subjectType` known to the store (as resource or
     * subject of a live relationship) that the check resolution grants on
     * `resource`, in order of first appearance, honoring `subjectRelation`, `wildcards` (the
     * `*` subject is left out only under `Wildcards.Exclude`), `limit` and
     * `cursor`. Excluded subjects are always empty.
     */
    lookupSubjects(request: LookupSubjectsRequest, options?: CallOptions): AsyncIterableIterator<LookupSubject> {
        const call = this.#record("lookupSubjects", request, options);
        requireObject(request, "lookupSubjects request");
        const fault = this.#faultAt(call, FaultStage.Call, 0);
        if (fault !== undefined)
            return failedStream(fault);
        const wildcards = mode(request.wildcards, Wildcards, Wildcards.Default, "wildcards");
        const limit = request.limit === undefined ? undefined : positiveLimit(request.limit, "limit");
        protoObject(request.resource, "resource");
        requireString(request.permission, "permission");
        requireString(request.subjectType, "subjectType");
        optionalString(request.subjectRelation, "subjectRelation");
        if (request.cursor !== undefined)
            requireString(request.cursor, "cursor");
        requireConsistency(request.consistency);
        if (request.context !== undefined)
            jsonSnapshot(request.context);
        this.#begin(options);
        const lookedUpAt = new ZedToken(String(this.#revision));
        const relation = directRelation(request.subjectRelation) === undefined ? {} : { relation: request.subjectRelation };
        const context = request.context === undefined ? {} : { context: request.context };
        return streamOf(() => this.#lookup(request.subjectType, request.cursor, limit,
            id => id === "*" && wildcards === Wildcards.Exclude ? "noPermission"
                : this.#resolve({ resource: request.resource, permission: request.permission, subject: { object: { objectType: request.subjectType, objectId: id }, ...relation }, consistency: request.consistency, ...context }),
            () => true)
            .map(([subjectId, permissionship]) => immutableSnapshot({ subject: { subjectId, permissionship, ...partialCaveat(permissionship) }, excludedSubjects: [], lookedUpAt }) as LookupSubject),
        index => this.#faultAt(call, FaultStage.Item, index));
    }
    async expandPermissionTree(request: ExpandPermissionTreeRequest, options?: CallOptions): Promise<ExpandResult> {
        return this.#unimplemented("expandPermissionTree", request, options);
    }
    async computablePermissions(request: ComputablePermissionsRequest, options?: CallOptions): Promise<RelationsResult> {
        return this.#unimplemented("computablePermissions", request, options);
    }
    async dependentRelations(request: DependentRelationsRequest, options?: CallOptions): Promise<RelationsResult> {
        return this.#unimplemented("dependentRelations", request, options);
    }
    /**
     * Diffs `comparisonSchema` against the stored schema text (an empty schema
     * before any write) at name level: identical text has no diffs; otherwise
     * definitions, relations and their subject types, permissions and their
     * expressions, caveats, caveat parameters and caveat expressions are
     * compared, with whitespace and comments ignored. It does not compile the
     * schema and reports no doc-comment changes. Unparseable text fails with
     * `InvalidArgument`, as the server's parse does.
     */
    async diffSchema(request: DiffSchemaRequest, options?: CallOptions): Promise<SchemaDiffResult> {
        this.#start("diffSchema", request, options);
        requireObject(request, "diffSchema request");
        requireConsistency(request.consistency);
        this.#begin(options);
        const existing = this.#schema ?? "";
        const revision = new ZedToken(String(this.#revision));
        if (existing === request.comparisonSchema)
            return immutableSnapshot({ diffs: [], revision });
        let diffs: SchemaDiff[];
        try {
            diffs = diffSchemas(existing, request.comparisonSchema);
        }
        catch (error) {
            if (error instanceof InvalidArgumentError)
                throw serverError(Code.InvalidArgument, error.message);
            throw error;
        }
        return immutableSnapshot({ diffs, revision });
    }
    watch(request?: WatchRequest, options?: CallOptions): AsyncIterableIterator<WatchEvent> {
        return this.#unimplementedStream("watch", request, options);
    }
    async experimentalRegisterRelationshipCounter(name: string, filter: RelationshipFilter, options?: CallOptions): Promise<void> {
        return this.#unimplemented("experimentalRegisterRelationshipCounter", { name, filter }, options, false);
    }
    async experimentalCountRelationships(name: string, options?: CallOptions): Promise<RelationshipCountResult> {
        return this.#unimplemented("experimentalCountRelationships", name, options);
    }
    async experimentalUnregisterRelationshipCounter(name: string, options?: CallOptions): Promise<void> {
        return this.#unimplemented("experimentalUnregisterRelationshipCounter", name, options, false);
    }
    async experimentalCountRelationshipsByFilter(filter: RelationshipFilter, options?: CallOptions): Promise<RelationshipCountResult> {
        return this.#unimplemented("experimentalCountRelationshipsByFilter", filter, options);
    }
    experimentalWatchPermissionSets(request?: WatchPermissionSetsRequest, options?: CallOptions): AsyncIterableIterator<PermissionSetEvent> {
        return this.#unimplementedStream("experimentalWatchPermissionSets", request, options);
    }
    experimentalLookupPermissionSets(request: LookupPermissionSetsRequest, options?: CallOptions): AsyncIterableIterator<LookupPermissionSet> {
        return this.#unimplementedStream("experimentalLookupPermissionSets", request, options);
    }
    async experimentalDownloadPermissionSets(request?: DownloadPermissionSetsRequest, options?: CallOptions): Promise<DownloadPermissionSetsResult> {
        return this.#unimplemented("experimentalDownloadPermissionSets", request, options);
    }
    async experimentalRoaringLookupResources(request: RoaringLookupResourcesRequest, options?: CallOptions): Promise<RoaringLookupResourcesResult> {
        return this.#unimplemented("experimentalRoaringLookupResources", request, options);
    }

    /**
     * Records a call without in-memory semantics, throws any injected fault,
     * applies the client's option checks (a never-idempotent mutation rejects
     * a retry opt-in), and fails with Unimplemented as a server would.
     */
    #unimplemented<K extends InMemoryOperation>(operation: K, request: InMemoryCallArguments[K]["request"], options: InMemoryCallArguments[K]["options"], idempotent = true): never {
        this.#start(operation, request, options);
        requireUnimplementedRequest(operation, request);
        if (!idempotent)
            mutationRetry(options?.retryPolicy, undefined, false, operation);
        this.#begin(options);
        throw unimplemented(operation);
    }
    /** The streaming form of `#unimplemented`: the failure surfaces from iteration. */
    #unimplementedStream<K extends InMemoryOperation, T>(operation: K, request: InMemoryCallArguments[K]["request"], options: InMemoryCallArguments[K]["options"]): AsyncIterableIterator<T> {
        const call = this.#record(operation, request, options);
        requireUnimplementedRequest(operation, request);
        const fault = this.#faultAt(call, FaultStage.Call, 0);
        if (fault !== undefined)
            return failedStream(fault);
        this.#begin(options);
        return failedStream(unimplemented(operation));
    }

    #check(request: CheckRequest, defaultContext: CheckRequest["context"]): CheckResult {
        requireConsistency(request.consistency);
        const context = defaultContext === undefined ? request.context : { ...defaultContext, ...request.context };
        return new CheckResult(this.#resolve(context === undefined ? request : { ...request, context }), [], new ZedToken(String(this.#revision)));
    }

    /**
     * Check resolution: the registered result, then the resolver, then the
     * stored relationships; otherwise no permission. Throws a registered error.
     */
    #resolve(request: CheckRequest): Permissionship {
        const registered = this.#checks.get(checkKey(request));
        if (registered instanceof SpiceDBError)
            throw registered;
        const resolved = registered ?? this.#resolver?.(request);
        // SpiceDB never answers with an unset permissionship (the client fails one), so the double never answers "unspecified".
        if (resolved === "unspecified")
            throw new InvalidArgumentError("check resolver returned \"unspecified\"; return a specified permissionship, or undefined to fall through to the stored relationships");
        return resolved ?? this.#stored(request);
    }
    /**
     * A live relationship on the resource whose relation is the permission's
     * name and whose subject is the checked one, or its type's wildcard for a
     * direct subject, grants; a caveated one grants conditionally.
     */
    #stored(request: CheckRequest): Permissionship {
        const relation = directRelation(request.subject.relation);
        let conditional = false;
        for (const [, rel] of this.#live()) {
            if (rel.resource.objectType !== request.resource.objectType || rel.resource.objectId !== request.resource.objectId || rel.relation !== request.permission)
                continue;
            const subject = rel.subject;
            if (subject.object.objectType !== request.subject.object.objectType || directRelation(subject.relation) !== relation)
                continue;
            if (subject.object.objectId !== request.subject.object.objectId && (subject.object.objectId !== "*" || relation !== undefined))
                continue;
            if (rel.caveat === undefined)
                return "hasPermission";
            conditional = true;
        }
        return conditional ? "conditionalPermission" : "noPermission";
    }
    /**
     * The known objects of `objectType` (in order of first appearance among the
     * live relationships, as resource or subject) that `resolve` grants,
     * after `cursor` and up to `limit` of those `include` keeps, each with its
     * permissionship and cursor: its 1-based position among the granted objects.
     */
    #lookup(objectType: string, cursor: string | undefined, limit: number | undefined, resolve: (id: string) => Permissionship, include: (permissionship: Permissionship) => boolean): [string, Permissionship, string][] {
        const start = cursor === undefined ? 0 : Number(cursor);
        if (!Number.isSafeInteger(start) || start < 0 || (cursor !== undefined && cursor.trim() === ""))
            throw serverError(Code.InvalidArgument, "invalid cursor");
        const ids = new Set<string>();
        for (const [, rel] of this.#live()) {
            if (rel.resource.objectType === objectType)
                ids.add(rel.resource.objectId);
            if (rel.subject.object.objectType === objectType)
                ids.add(rel.subject.object.objectId);
        }
        const out: [string, Permissionship, string][] = [];
        let position = 0;
        for (const id of ids) {
            const permissionship = resolve(id);
            if (permissionship !== "hasPermission" && permissionship !== "conditionalPermission")
                continue;
            position++;
            if (position <= start || !include(permissionship))
                continue;
            if (limit !== undefined && out.length >= limit)
                break;
            out.push([id, permissionship, String(position)]);
        }
        return out;
    }

    /** Records a call. */
    #record<K extends InMemoryOperation>(operation: K, request: InMemoryCallArguments[K]["request"], options: InMemoryCallArguments[K]["options"]): RecordedCall {
        const index = this.#counts.get(operation) ?? 0;
        this.#counts.set(operation, index + 1);
        const call = Object.freeze({ operation, index, request, options: options === undefined ? undefined : Object.freeze({ ...options }) }) as unknown as RecordedCall;
        this.#calls.push(call);
        return call;
    }
    /** Records a call and throws any fault injected before it. */
    #start<K extends InMemoryOperation>(operation: K, request: InMemoryCallArguments[K]["request"], options: InMemoryCallArguments[K]["options"]): RecordedCall {
        const call = this.#record(operation, request, options);
        this.#fault(call, FaultStage.Call, 0);
        return call;
    }
    /** The error the fault hook injects at one point of a call, if any. */
    #faultAt(call: RecordedCall, stage: FaultStage, item: number, batch?: readonly Relationship[]): Error | undefined {
        return this.#hook?.(Object.freeze({ call, stage, item, ...(batch === undefined ? {} : { batch: Object.freeze([...batch]) }) })) ?? undefined;
    }
    #fault(call: RecordedCall, stage: FaultStage, item: number, batch?: readonly Relationship[]): void {
        const error = this.#faultAt(call, stage, item, batch);
        if (error !== undefined)
            throw error;
    }
    #begin(options?: CallOptions): void {
        if (this.#closed)
            throw new SpiceDBError("Client is closed");
        if (options?.retryPolicy !== undefined)
            validRetryPolicy(options.retryPolicy);
        if (options?.signal?.aborted)
            throw new OperationAbortError(options.signal.reason);
        options?.onHeader?.(new Headers(this.#header));
        options?.onTrailer?.(new Headers(this.#trailer));
    }
    #bump(): ZedToken { this.#revision++; return new ZedToken(String(this.#revision)); }
    #isLive(rel: Relationship | undefined): boolean { return rel !== undefined && (rel.expiration === undefined || rel.expiration.getTime() > Date.now()); }
    *#live(map = this.#relationships): Generator<[string, Relationship]> {
        for (const key of [...map.keys()].sort())
            if (this.#isLive(map.get(key)))
                yield [key, map.get(key)!];
    }
    /** Matching live relationships after `cursor`, up to `limit`, each with its cursor: its 1-based position among the matches. */
    #page(matches: (rel: Relationship) => boolean, cursor: string | undefined, limit: number | undefined): [Relationship, string][] {
        const start = cursor === undefined ? 0 : Number(cursor);
        if (!Number.isSafeInteger(start) || start < 0 || (cursor !== undefined && cursor.trim() === ""))
            throw serverError(Code.InvalidArgument, "invalid cursor");
        const out: [Relationship, string][] = [];
        let position = 0;
        for (const [, rel] of this.#live()) {
            if (!matches(rel))
                continue;
            position++;
            if (position <= start)
                continue;
            if (limit !== undefined && out.length >= limit)
                break;
            out.push([rel, String(position)]);
        }
        return out;
    }
    #preconditions(map: Map<string, Relationship>, preconditions: readonly Precondition[]): void {
        const any = (filter: RelationshipFilter) => [...this.#live(map)].some(([, rel]) => filterMatches(filter, rel));
        for (const p of preconditions)
            if (any(p.filter) !== (p.operation === "mustMatch"))
                throw serverError(Code.FailedPrecondition, `unable to satisfy write precondition \`${preconditionToString(p)}\``, { reason: "ERROR_REASON_WRITE_OR_DELETE_PRECONDITION_FAILURE", reasonDomain: "authzed.com" });
    }
}

/**
 * The error the real client raises when SpiceDB answers with `code` and
 * `message`: Connect's prefixed message, the server message verbatim on
 * `serverMessage`, and the `ConnectError` as `cause`.
 */
function serverError(code: Code, message: string, options?: Omit<SpiceDBErrorOptions, "code" | "cause" | "serverMessage">): SpiceDBError {
    const raw = new ConnectError(message, code);
    return SpiceDBError.fromCode(code, raw.message, { ...options, cause: raw, serverMessage: message });
}
/** The failure SpiceDB reports for an operation the double does not implement, shaped like the client's. */
function unimplemented(operation: InMemoryOperation): SpiceDBError {
    return serverError(Code.Unimplemented, `${operation} is not implemented by InMemorySpiceDB`);
}
/** A stream that fails with `error` when iterated, as a server failure does. */
function failedStream<T>(error: Error): AsyncIterableIterator<T> {
    return (async function* (): AsyncGenerator<T> { throw error; })();
}
/**
 * Sets (or clears) the `resumeCursor` a failed resumable read reports, as the
 * real client does: the cursor after the last delivered item, the request's
 * own cursor when nothing was delivered yet, and absent when the read should
 * restart from the beginning.
 */
function withResumeCursor<E>(error: E, cursor: string | undefined): E {
    if (!(error instanceof Error))
        return error;
    try {
        if (cursor !== undefined)
            Object.defineProperty(error, "resumeCursor", { value: cursor, enumerable: true, configurable: true });
        else if (Object.prototype.hasOwnProperty.call(error, "resumeCursor"))
            Reflect.deleteProperty(error, "resumeCursor");
    }
    catch {
        // A frozen caller-owned error keeps whatever it had.
    }
    return error;
}
/**
 * Snapshots the items now; a server error (such as an invalid cursor) surfaces
 * from iteration, as the real client's does. `fault` is consulted before each item.
 * With `resume` (a resumable paged read), every error from iteration carries
 * the `resumeCursor` the real client would report: starting from the
 * request's cursor, advanced to each delivered item's `afterResultCursor`.
 */
function streamOf<T>(items: () => T[], fault: (index: number) => Error | undefined, resume?: { cursor: string | undefined }): AsyncIterableIterator<T> {
    let snapshot: T[] | undefined;
    let failure: unknown;
    try {
        snapshot = items();
    }
    catch (error) {
        failure = error;
    }
    let cursor = resume?.cursor;
    const fail = (error: unknown): unknown => resume === undefined ? error : withResumeCursor(error, cursor);
    return (async function* () {
        if (snapshot === undefined)
            throw fail(failure);
        for (const [index, item] of snapshot.entries()) {
            const error = fault(index);
            if (error !== undefined)
                throw fail(error);
            yield item;
            const after = (item as { afterResultCursor?: string }).afterResultCursor;
            if (after)
                cursor = after;
        }
    })();
}
function snapshot(rel: Relationship): Relationship { return immutableSnapshot(nativeRelationshipFields(rel)); }
function identity(rel: Relationship): string { return JSON.stringify(relationshipToFilter(rel)); }
function checkKey(c: CheckRegistration): string {
    const relation = directRelation(c.subject.relation);
    return `${objectRefToString(c.resource)}#${c.permission}@${subjectRefToString(relation === undefined ? { object: c.subject.object } : { object: c.subject.object, relation })}`;
}
/**
 * The partial-caveat info a conditional lookup result carries, as the real
 * client's do. The double does not evaluate caveats, so it names no missing
 * context.
 */
function partialCaveat(permissionship: Permissionship): { partialCaveat?: { missingRequiredContext: readonly string[] } } {
    return permissionship === "conditionalPermission" ? { partialCaveat: { missingRequiredContext: [] } } : {};
}
/** A subject relation, with `...` and the empty string (direct subjects) as `undefined`. */
function directRelation(relation: string | undefined): string | undefined {
    return relation === undefined || relation === "" || relation === "..." ? undefined : relation;
}
/**
 * The real client's presence checks for an operation the double does not
 * implement: a required request object (or name and filter) is checked
 * before the call fails with Unimplemented, so a missing one is
 * `InvalidArgumentError` here too. Optional requests (watches, downloads)
 * may be absent.
 */
function requireUnimplementedRequest(operation: InMemoryOperation, request: unknown): void {
    switch (operation) {
        case "expandPermissionTree":
        case "computablePermissions":
        case "dependentRelations":
        case "experimentalLookupPermissionSets":
        case "experimentalRoaringLookupResources":
            requireObject(request, `${operation} request`);
            return;
        case "experimentalCountRelationships":
        case "experimentalUnregisterRelationshipCounter":
            requireString(request, "name");
            return;
        case "experimentalRegisterRelationshipCounter": {
            const { name, filter } = request as { name: unknown; filter: RelationshipFilter };
            requireString(name, "name");
            toProtoRelationshipFilter(filter);
            return;
        }
        case "experimentalCountRelationshipsByFilter":
            toProtoRelationshipFilter(request as RelationshipFilter);
            return;
        case "watch": {
            const watch = request as WatchRequest | undefined | null;
            [...(watch?.objectTypes ?? [])].forEach((objectType, index) => requireString(objectType, `objectTypes[${index}]`));
            watch?.relationshipFilters?.forEach(f => toProtoRelationshipFilter(f));
            return;
        }
        default:
            return;
    }
}
function requireConsistency(value: Consistency): void {
    if (!(value instanceof Consistency))
        throw new InvalidArgumentError("consistency is required");
}

/** The real client's request-shape checks for one check: string names and IDs, else `InvalidArgumentError`. */
function requireCheckShape(request: CheckRequest, prefix: string): void {
    protoObject(request.resource, `${prefix}resource`);
    requireString(request.permission, `${prefix}permission`);
    protoSubject(request.subject);
}
