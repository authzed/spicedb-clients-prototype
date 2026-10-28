import { Code } from "@connectrpc/connect";
import { Consistency } from "./consistency.js";
import { InvalidArgumentError, SpiceDBError } from "./errors.js";
import { DeletePartial, mode } from "./modes.js";
import { OperationAbortError } from "./operation.js";
import { ZedToken } from "./token.js";
import {
    CheckResult, Transaction, filterMatches, filterToString, immutableSnapshot, nativeRelationshipFields, objectRefToString, positiveLimit, relationshipToFilter,
    relationshipToString, subjectRefToString, toProtoRelationshipFilter,
    type CallOptions, type CheckOptions, type CheckRequest, type DeleteAllOptions, type DeleteOptions, type DeleteResult, type ObjectRef,
    type Permissionship, type Preconditions, type ReadRelationship, type ReadRelationshipsRequest, type Relationship,
    type RelationshipFilter, type SubjectRef, type WriteResult,
} from "./types.js";

/** Identifies a check whose result {@link InMemorySpiceDB.setCheckResult} registers. */
export interface CheckRegistration {
    readonly resource: ObjectRef;
    readonly permission: string;
    readonly subject: SubjectRef;
}

/**
 * An in-memory test double for the relationship and check operations of
 * {@link SpiceDBClient}. Its `write`, `readRelationships`,
 * `deleteRelationships`, `deleteAllRelationshipsNonAtomic`, `check`,
 * `checkBulk` and `close` have the client's signatures, so a caller-defined
 * type such as `Pick<SpiceDBClient, "write" | "check">` accepts both.
 *
 * It applies SpiceDB's relationship semantics: atomic transactions,
 * preconditions (`FailedPreconditionError`), CREATE conflicts
 * (`AlreadyExistsError`), filter matching via {@link filterMatches}, bounded
 * and all-or-nothing deletes, and expired relationships disappearing from
 * reads. It does not evaluate a schema: checks return results registered with
 * {@link InMemorySpiceDB.setCheckResult}, and unregistered checks are
 * `noPermission`. Errors are the same native errors the real client raises.
 */
export class InMemorySpiceDB {
    #relationships = new Map<string, Relationship>();
    #checks = new Map<string, Permissionship | SpiceDBError>();
    #revision = 0;
    #closed = false;

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

    async write(transaction: Transaction, options?: CallOptions): Promise<WriteResult> {
        this.#begin(options);
        if (!(transaction instanceof Transaction))
            throw new InvalidArgumentError("write requires a Transaction");
        const next = new Map(this.#relationships);
        this.#preconditions(next, { mustMatch: transaction.preconditions.filter(p => p.operation === "mustMatch").map(p => p.filter), mustNotMatch: transaction.preconditions.filter(p => p.operation === "mustNotMatch").map(p => p.filter) });
        for (const update of transaction.updates) {
            const key = identity(update.relationship);
            if (update.operation === "create" && this.#isLive(next.get(key)))
                throw SpiceDBError.fromCode(Code.AlreadyExists, `could not CREATE relationship \`${relationshipToString(update.relationship)}\`, as it already existed`);
            if (update.operation === "delete")
                next.delete(key);
            else
                next.set(key, update.relationship);
        }
        this.#relationships = next;
        return immutableSnapshot({ revision: this.#bump() });
    }

    readRelationships(request: ReadRelationshipsRequest, options?: CallOptions): AsyncIterableIterator<ReadRelationship> {
        this.#begin(options);
        requireConsistency(request.consistency);
        toProtoRelationshipFilter(request.filter);
        const limit = request.limit === undefined ? undefined : positiveLimit(request.limit, "limit");
        const readAt = new ZedToken(String(this.#revision));
        const matches = [...this.#live()].filter(([, rel]) => filterMatches(request.filter, rel)).slice(0, limit)
            .map(([, rel]) => immutableSnapshot({ ...rel, readAt }) as ReadRelationship);
        return (async function* () { yield* matches; })();
    }

    async deleteRelationships(filter: RelationshipFilter, options: DeleteOptions): Promise<DeleteResult> {
        this.#begin(options);
        toProtoRelationshipFilter(filter);
        const partial = mode(options?.partial, DeletePartial, DeletePartial.Allowed, "partial");
        const limit = partial === DeletePartial.Disallowed && options.limit === undefined ? undefined : positiveLimit(options.limit as number, "limit");
        const next = new Map(this.#relationships);
        this.#preconditions(next, options);
        const matches = [...this.#live(next)].filter(([, rel]) => filterMatches(filter, rel)).map(([key]) => key);
        if (partial === DeletePartial.Disallowed && limit !== undefined && matches.length > limit)
            throw SpiceDBError.fromCode(Code.InvalidArgument, `found more than ${limit} relationships to be deleted and partial deletion was not requested`);
        const deleted = limit === undefined ? matches : matches.slice(0, limit);
        for (const key of deleted)
            next.delete(key);
        this.#relationships = next;
        return immutableSnapshot({ revision: this.#bump(), deletedCount: BigInt(deleted.length), progress: deleted.length < matches.length ? "partial" : "complete" });
    }

    async deleteAllRelationshipsNonAtomic(filter: RelationshipFilter, options?: DeleteAllOptions): Promise<DeleteResult> {
        const pageSize = positiveLimit(options?.pageSize ?? 1000, "pageSize");
        let total = 0n;
        for (;;) {
            const result = await this.deleteRelationships(filter, { ...options, limit: pageSize });
            total += result.deletedCount;
            const progress: DeleteResult = immutableSnapshot({ ...result, deletedCount: total });
            options?.onProgress?.(progress);
            if (progress.progress === "complete")
                return progress;
        }
    }

    async check(request: CheckRequest, options?: CheckOptions): Promise<CheckResult> {
        this.#begin(options);
        requireConsistency(request.consistency);
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
        if (options?.signal?.aborted)
            throw new OperationAbortError(options.signal.reason);
    }
    #bump(): ZedToken { this.#revision++; return new ZedToken(String(this.#revision)); }
    #isLive(rel: Relationship | undefined): boolean { return rel !== undefined && (rel.expiration === undefined || rel.expiration.getTime() > Date.now()); }
    *#live(map = this.#relationships): Generator<[string, Relationship]> {
        for (const key of [...map.keys()].sort())
            if (this.#isLive(map.get(key)))
                yield [key, map.get(key)!];
    }
    #preconditions(map: Map<string, Relationship>, preconditions: Preconditions | undefined): void {
        const any = (filter: RelationshipFilter) => [...this.#live(map)].some(([, rel]) => filterMatches(filter, rel));
        for (const filter of preconditions?.mustMatch ?? [])
            if (!any(filter))
                throw SpiceDBError.fromCode(Code.FailedPrecondition, `unable to satisfy write precondition: MUST_MATCH no relationships matched ${filterToString(filter)}`);
        for (const filter of preconditions?.mustNotMatch ?? [])
            if (any(filter))
                throw SpiceDBError.fromCode(Code.FailedPrecondition, `unable to satisfy write precondition: MUST_NOT_MATCH a relationship matched ${filterToString(filter)}`);
    }
}

function snapshot(rel: Relationship): Relationship { return immutableSnapshot(nativeRelationshipFields(rel)); }
function identity(rel: Relationship): string { return JSON.stringify(relationshipToFilter(rel)); }
function checkKey(c: CheckRegistration): string { return `${objectRefToString(c.resource)}#${c.permission}@${subjectRefToString(c.subject)}`; }
function requireConsistency(value: Consistency): void {
    if (!(value instanceof Consistency))
        throw new InvalidArgumentError("consistency is required");
}
