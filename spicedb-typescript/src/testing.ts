import { Code, ConnectError } from "@connectrpc/connect";
import { Consistency } from "./consistency.js";
import { InvalidArgumentError, SpiceDBError, type SpiceDBErrorOptions } from "./errors.js";
import { DeletePartial, mode } from "./modes.js";
import { OperationAbortError } from "./operation.js";
import { mutationRetry, validRetryPolicy } from "./retry.js";
import { ZedToken } from "./token.js";
import {
    CheckResult, Transaction, filterMatches, immutableSnapshot, nativeRelationshipFields, objectRefToString, positiveLimit, preconditionToString, relationshipToFilter,
    relationshipToString, subjectRefToString, toProtoRelationship, toProtoRelationshipFilter,
    type CallOptions, type CheckOptions, type CheckRequest, type DeleteAllOptions, type DeleteOptions, type DeleteResult, type ExportedRelationship,
    type ExportRelationshipsRequest, type ImportOptions, type ObjectRef, type Permissionship, type Precondition, type ReadRelationship,
    type ReadRelationshipsRequest, type ReflectSchemaRequest, type ReflectSchemaResult, type Relationship, type RelationshipFilter, type SchemaResult,
    type SubjectRef, type WriteResult,
} from "./types.js";

/** Identifies a check whose result {@link InMemorySpiceDB.setCheckResult} registers. */
export interface CheckRegistration {
    readonly resource: ObjectRef;
    readonly permission: string;
    readonly subject: SubjectRef;
}

/**
 * An in-memory test double for {@link SpiceDBClient}. Its `write`,
 * `readRelationships`, `deleteRelationships`, `deleteAllRelationshipsNonAtomic`,
 * `importRelationships`, `exportRelationships`, `readSchema`, `writeSchema`,
 * `reflectSchema`, `check`, `checkBulk` and `close` have the client's
 * signatures, so a caller-defined type such as
 * `Pick<SpiceDBClient, "write" | "check">` accepts both.
 *
 * It applies SpiceDB's relationship semantics: atomic transactions,
 * preconditions (`FailedPreconditionError`), CREATE conflicts and duplicate
 * imports (`AlreadyExistsError`), empty writes (preconditions checked, current
 * revision returned), filter matching via {@link filterMatches}, bounded and
 * all-or-nothing deletes, and expired relationships disappearing from reads.
 * It does not evaluate a schema: checks return results registered with
 * {@link InMemorySpiceDB.setCheckResult} (unregistered checks are
 * `noPermission`), `writeSchema` stores the text without compiling it, and
 * `reflectSchema` returns the result registered with
 * {@link InMemorySpiceDB.setReflectSchemaResult}.
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
 * the store's identity order.
 */
export class InMemorySpiceDB {
    #relationships = new Map<string, Relationship>();
    #checks = new Map<string, Permissionship | SpiceDBError>();
    #revision = 0;
    #closed = false;
    #schema?: string;
    #reflection?: Omit<ReflectSchemaResult, "revision">;
    #header?: HeadersInit;
    #trailer?: HeadersInit;

    constructor(initial: Iterable<Relationship> = []) {
        for (const rel of initial)
            this.#relationships.set(identity(rel), snapshot(rel));
    }

    /** Every live relationship, in identity order. */
    get relationships(): readonly Relationship[] { return Object.freeze([...this.#live()].map(([, rel]) => rel)); }

    /** Registers the result (or error) a check of `registration` returns. */
    setCheckResult(registration: CheckRegistration, result: Permissionship | SpiceDBError): void {
        this.#checks.set(checkKey(registration), result);
    }

    /** Registers the definitions and caveats `reflectSchema` returns (stamped with the current revision). */
    setReflectSchemaResult(result: Omit<ReflectSchemaResult, "revision">): void {
        this.#reflection = immutableSnapshot({ definitions: result.definitions, caveats: result.caveats });
    }

    /** Sets the headers and trailers every operation reports to its `onHeader`/`onTrailer` callbacks. */
    setResponseMetadata(header?: HeadersInit, trailer?: HeadersInit): void {
        this.#header = header === undefined ? undefined : new Headers(header);
        this.#trailer = trailer === undefined ? undefined : new Headers(trailer);
    }

    /** Applies the transaction atomically; an empty transaction checks its preconditions and returns the current revision, as SpiceDB does. */
    async write(transaction: Transaction, options?: CallOptions): Promise<WriteResult> {
        if (!(transaction instanceof Transaction))
            throw new InvalidArgumentError("write requires a Transaction");
        mutationRetry(options?.retryPolicy, transaction.preconditions.length === 0 && transaction.updates.every(u => u.operation === "touch"), "write");
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
        requireConsistency(request.consistency);
        toProtoRelationshipFilter(request.filter);
        const limit = request.limit === undefined ? undefined : positiveLimit(request.limit, "limit");
        this.#begin(options);
        const readAt = new ZedToken(String(this.#revision));
        return streamOf(() => this.#page(rel => filterMatches(request.filter, rel), request.cursor, limit)
            .map(([rel, afterResultCursor]) => immutableSnapshot({ ...rel, afterResultCursor, readAt }) as ReadRelationship));
    }

    /** Yields every relationship (or those matching `filter`) after `cursor`; each carries its `afterResultCursor`. */
    exportRelationships(request: ExportRelationshipsRequest, options?: CallOptions): AsyncIterableIterator<ExportedRelationship> {
        requireConsistency(request.consistency);
        if (request.filter !== undefined)
            toProtoRelationshipFilter(request.filter);
        positiveLimit(request.pageSize ?? 1000, "pageSize");
        this.#begin(options);
        const filter = request.filter;
        return streamOf(() => this.#page(rel => filter === undefined || filterMatches(filter, rel), request.cursor, undefined)
            .map(([rel, afterResultCursor]) => immutableSnapshot({ ...rel, afterResultCursor }) as ExportedRelationship));
    }

    /** Adds every relationship in one transaction, as SpiceDB does: if any already exists (or repeats), nothing is added (`AlreadyExistsError`). */
    async importRelationships(relationships: Iterable<Relationship> | AsyncIterable<Relationship>, options?: ImportOptions): Promise<bigint> {
        mutationRetry(options?.retryPolicy, false, "importRelationships");
        positiveLimit(options?.batchSize ?? 1000, "batchSize");
        this.#begin(options);
        const batch: Relationship[] = [];
        for await (const rel of relationships) {
            toProtoRelationship(rel);
            batch.push(snapshot(rel));
        }
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
        mutationRetry(options?.retryPolicy, false, "deleteRelationships");
        toProtoRelationshipFilter(filter);
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

    async deleteAllRelationshipsNonAtomic(filter: RelationshipFilter, options?: DeleteAllOptions): Promise<DeleteResult> {
        mutationRetry(options?.retryPolicy, false, "deleteAllRelationshipsNonAtomic");
        const pageSize = positiveLimit(options?.pageSize ?? 1000, "pageSize");
        let total = 0n;
        for (;;) {
            const result = await this.deleteRelationships(filter, { ...options, retryPolicy: undefined, limit: pageSize });
            total += result.deletedCount;
            const progress: DeleteResult = immutableSnapshot({ ...result, deletedCount: total });
            options?.onProgress?.(progress);
            if (progress.progress === "complete")
                return progress;
        }
    }

    /** The schema last written, or `NotFoundError` as SpiceDB reports before any schema exists. */
    async readSchema(options?: CallOptions): Promise<SchemaResult> {
        this.#begin(options);
        if (this.#schema === undefined)
            throw serverError(Code.NotFound, "No schema has been defined; please call WriteSchema to start");
        return immutableSnapshot({ schema: this.#schema, revision: new ZedToken(String(this.#revision)) });
    }

    /** Stores the schema text; it is not compiled or validated. */
    async writeSchema(schema: string, options?: CallOptions): Promise<ZedToken> {
        mutationRetry(options?.retryPolicy, true, "writeSchema");
        this.#begin(options);
        this.#schema = schema;
        return this.#bump();
    }

    /** The result registered with {@link InMemorySpiceDB.setReflectSchemaResult}, or `NotFoundError` without one. */
    async reflectSchema(request: ReflectSchemaRequest, options?: CallOptions): Promise<ReflectSchemaResult> {
        requireConsistency(request.consistency);
        this.#begin(options);
        if (this.#reflection === undefined)
            throw serverError(Code.NotFound, "No schema has been defined; please call WriteSchema to start");
        return immutableSnapshot({ ...this.#reflection, revision: new ZedToken(String(this.#revision)) });
    }

    async check(request: CheckRequest, options?: CheckOptions): Promise<CheckResult> {
        requireConsistency(request.consistency);
        this.#begin(options);
        const registered = this.#checks.get(checkKey(request));
        if (registered instanceof SpiceDBError)
            throw registered;
        return new CheckResult(registered ?? "noPermission", [], new ZedToken(String(this.#revision)));
    }

    async checkBulk(requests: readonly CheckRequest[], options?: CheckOptions): Promise<readonly CheckResult[]> {
        this.#begin(options);
        const results: CheckResult[] = [];
        for (const [index, request] of requests.entries()) {
            try {
                results.push(await this.check(request));
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
/** Snapshots the items now; a server error (such as an invalid cursor) surfaces from iteration, as the real client's does. */
function streamOf<T>(items: () => T[]): AsyncIterableIterator<T> {
    let snapshot: T[] | undefined;
    let failure: unknown;
    try {
        snapshot = items();
    }
    catch (error) {
        failure = error;
    }
    return (async function* () {
        if (snapshot === undefined)
            throw failure;
        yield* snapshot;
    })();
}
function snapshot(rel: Relationship): Relationship { return immutableSnapshot(nativeRelationshipFields(rel)); }
function identity(rel: Relationship): string { return JSON.stringify(relationshipToFilter(rel)); }
function checkKey(c: CheckRegistration): string { return `${objectRefToString(c.resource)}#${c.permission}@${subjectRefToString(c.subject)}`; }
function requireConsistency(value: Consistency): void {
    if (!(value instanceof Consistency))
        throw new InvalidArgumentError("consistency is required");
}
