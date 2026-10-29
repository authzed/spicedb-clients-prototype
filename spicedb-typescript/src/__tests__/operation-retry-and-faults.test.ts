// Client-wide per-operation retry policies, the public idempotence rule, and
// the test double's call recording and fault injection (root DESIGN.md: "Retry
// only when replay is safe", "Make native values observable and testable").
import { describe, it, expect } from "vitest";
import { ConnectError, Code, createRouterTransport, type ConnectRouter } from "@connectrpc/connect";
import { create } from "@bufbuild/protobuf";
import { PermissionsService, SchemaService, WatchService, ReadSchemaResponseSchema, WriteSchemaResponseSchema, WriteRelationshipsResponseSchema, ZedTokenSchema } from "@spicedb/proto";
import {
    SpiceDBClient, Transaction, relationship, full, InvalidArgumentError, UnavailableError, AlreadyExistsError, Retries, StreamRetries, DEFAULT_RETRY_POLICY,
    InMemorySpiceDB, FaultStage, type RetryPolicy, type OperationName, type Fault, type Relationship,
} from "../index.js";

const docViewer = (doc: string, user: string) => relationship(`document:${doc}`, "viewer", `user:${user}`);
const retryOnce: RetryPolicy = { maxRetries: 1, retryableCodes: [Code.Unavailable], initialBackoffMs: 1 };
const noRetries: RetryPolicy = { ...DEFAULT_RETRY_POLICY, maxRetries: 0 };
const retryThrice: RetryPolicy = { ...DEFAULT_RETRY_POLICY, initialBackoffMs: 1 };
const drain = async <T>(it: AsyncIterable<T>) => { const out: T[] = []; for await (const x of it) out.push(x); return out; };

function flakyServer(options: { writeFailures?: number } = {}) {
    const calls = { write: 0, schema: 0, read: 0, watch: 0, readSchema: 0 };
    const transport = createRouterTransport(({ service }: ConnectRouter) => {
        service(PermissionsService, {
            writeRelationships() { if (calls.write++ < (options.writeFailures ?? 1)) throw new ConnectError("transient", Code.Unavailable); return create(WriteRelationshipsResponseSchema, { writtenAt: create(ZedTokenSchema, { token: "w" }) }); },
            async *readRelationships() { calls.read++; throw new ConnectError("down", Code.Unavailable); },
        });
        service(SchemaService, {
            writeSchema() { if (calls.schema++ === 0) throw new ConnectError("transient", Code.Unavailable); return create(WriteSchemaResponseSchema, { writtenAt: create(ZedTokenSchema, { token: "s" }) }); },
            readSchema() { calls.readSchema++; throw new ConnectError("down", Code.Unavailable); },
        });
        service(WatchService, { async *watch() { calls.watch++; throw new ConnectError("down", Code.Unavailable); } });
    });
    return { transport, calls };
}

describe("client-wide per-operation retry policy", () => {
    it("never reopens a watch when the client sets a zero-retry watch policy, with no per-call option", async () => {
        const { transport, calls } = flakyServer();
        const client = SpiceDBClient.fromTransport(transport, { retryPolicy: retryThrice, operationRetryPolicies: { watch: noRetries, readRelationships: noRetries } });
        await expect(drain(client.watch())).rejects.toBeInstanceOf(UnavailableError);
        expect(calls.watch).toBe(1);
        // A stream that the client would otherwise retry is opened once too...
        await expect(drain(client.readRelationships({ filter: { resourceType: "document" }, consistency: full() }))).rejects.toBeInstanceOf(UnavailableError);
        expect(calls.read).toBe(1);
        // ...while other operations keep the client-wide policy.
        await expect(client.readSchema()).rejects.toBeInstanceOf(UnavailableError);
        expect(calls.readSchema).toBe(4);
    });

    it("is overridden by a per-call policy, and overrides the client-wide settings", async () => {
        let { transport, calls } = flakyServer();
        let client = SpiceDBClient.fromTransport(transport, { operationRetryPolicies: { watch: noRetries } });
        await expect(drain(client.watch(undefined, { retryPolicy: retryOnce }))).rejects.toBeInstanceOf(UnavailableError);
        expect(calls.watch).toBe(2);

        // It governs stream establishment, so it can opt watch into reopening and
        // survives StreamRetries.Disabled and Retries.Disabled.
        ({ transport, calls } = flakyServer());
        client = SpiceDBClient.fromTransport(transport, { retries: Retries.Disabled, streamRetries: StreamRetries.Disabled, operationRetryPolicies: { watch: retryOnce, readRelationships: retryOnce, readSchema: retryOnce } });
        await expect(drain(client.watch())).rejects.toBeInstanceOf(UnavailableError);
        expect(calls.watch).toBe(2);
        await expect(drain(client.readRelationships({ filter: { resourceType: "document" }, consistency: full() }))).rejects.toBeInstanceOf(UnavailableError);
        expect(calls.read).toBe(2);
        await expect(client.readSchema()).rejects.toBeInstanceOf(UnavailableError);
        expect(calls.readSchema).toBe(2);
    });

    it("retries only idempotent writes, without rejecting the others", async () => {
        const { transport, calls } = flakyServer({ writeFailures: Infinity });
        const client = SpiceDBClient.fromTransport(transport, { operationRetryPolicies: { write: retryOnce, writeSchema: retryOnce } });
        await expect(client.write(new Transaction().touch(docViewer("a", "alice")))).rejects.toBeInstanceOf(UnavailableError);
        expect(calls.write).toBe(2);
        await expect(client.write(new Transaction().create(docViewer("a", "alice")))).rejects.toBeInstanceOf(UnavailableError);
        expect(calls.write).toBe(3);
        await expect(client.write(new Transaction().touch(docViewer("a", "alice")).mustMatch({ resourceType: "document" }))).rejects.toBeInstanceOf(UnavailableError);
        expect(calls.write).toBe(4);
        expect((await client.writeSchema("definition user {}")).toString()).toBe("s");
        expect(calls.schema).toBe(2);
        // A per-call zero-retry policy still wins.
        await expect(client.write(new Transaction().touch(docViewer("a", "alice")), { retryPolicy: noRetries })).rejects.toBeInstanceOf(UnavailableError);
        expect(calls.write).toBe(5);
    });

    it("rejects retries for never-idempotent operations when the client is built", () => {
        const { transport } = flakyServer();
        const never: OperationName[] = ["deleteRelationships", "deleteAllRelationshipsNonAtomic", "importRelationships", "experimentalRegisterRelationshipCounter", "experimentalUnregisterRelationshipCounter"];
        for (const name of never) {
            expect(() => SpiceDBClient.fromTransport(transport, { operationRetryPolicies: { [name]: retryOnce } })).toThrow(InvalidArgumentError);
            expect(() => new SpiceDBClient({ endpoint: "localhost:1", token: "t", operationRetryPolicies: { [name]: retryOnce } })).toThrow(`${name} cannot be retried`);
            // A zero-retry policy is allowed for any operation.
            expect(() => SpiceDBClient.fromTransport(transport, { operationRetryPolicies: { [name]: noRetries } })).not.toThrow();
        }
        expect(() => SpiceDBClient.fromTransport(transport, { operationRetryPolicies: { raw: noRetries } as never })).toThrow("raw is not a client operation");
        // Private helpers and inherited members are methods too, but not operations.
        for (const name of ["stream", "unary", "retryOrThrow", "operation", "effectiveTimeoutMs", "close", "constructor", "toString", "__proto__", "Write"])
            expect(() => SpiceDBClient.fromTransport(transport, { operationRetryPolicies: JSON.parse(`{${JSON.stringify(name)}: ${JSON.stringify(noRetries)}}`) })).toThrow(`${name} is not a client operation`);
        expect(() => SpiceDBClient.fromTransport(transport, { operationRetryPolicies: { readSchema: { ...retryOnce, initialBackoffMs: 0 } } })).toThrow(InvalidArgumentError);
    });

    it("names exactly the client's operations", () => {
        type Methods = { [K in keyof SpiceDBClient]: SpiceDBClient[K] extends (...args: never[]) => unknown ? K : never }[keyof SpiceDBClient];
        type Assert<T extends true> = T;
        type Same = Assert<[Exclude<Methods, "raw" | "close">] extends [OperationName] ? [OperationName] extends [Exclude<Methods, "raw" | "close">] ? true : false : false>;
        const same: Same = true;
        expect(same).toBe(true);
    });
});

describe("Transaction.isIdempotent", () => {
    it("holds only for touch-only transactions without preconditions", () => {
        expect(new Transaction().isIdempotent()).toBe(true);
        expect(new Transaction().touch(docViewer("a", "b")).touch(docViewer("c", "d")).isIdempotent()).toBe(true);
        expect(new Transaction().create(docViewer("a", "b")).isIdempotent()).toBe(false);
        expect(new Transaction().delete(docViewer("a", "b")).isIdempotent()).toBe(false);
        expect(new Transaction().touch(docViewer("a", "b")).mustNotMatch({ resourceType: "document" }).isIdempotent()).toBe(false);
    });
});

function* tuples(n: number): Generator<Relationship> {
    for (let i = 0; i < n; i++)
        yield docViewer(`d${i}`, "x");
}

describe("InMemorySpiceDB fault injection", () => {
    it("fails an import batch with already-exists and applies nothing", async () => {
        const db = new InMemorySpiceDB();
        const seen: Fault[] = [];
        db.setFaultHook(fault => {
            seen.push(fault);
            if (fault.call.operation === "importRelationships" && fault.stage === FaultStage.ImportBatch && fault.item === 2)
                return InMemorySpiceDB.serverError(Code.AlreadyExists, "duplicate relationship");
            return undefined;
        });
        const error = await db.importRelationships(tuples(10), { batchSize: 3 }).catch((e: unknown) => e);
        expect(error).toBeInstanceOf(AlreadyExistsError);
        expect((error as AlreadyExistsError).message).toBe("[already_exists] duplicate relationship");
        expect((error as AlreadyExistsError).serverMessage).toBe("duplicate relationship");
        expect(db.relationships).toEqual([]);
        expect(seen.map(f => `${f.stage}:${f.item}`)).toEqual(["call:0", "importBatch:0", "importBatch:1", "importBatch:2"]);

        // The commit itself can fail, after every batch.
        db.setFaultHook(fault => fault.stage === FaultStage.ImportCommit ? InMemorySpiceDB.serverError(Code.Unavailable, "lost") : undefined);
        await expect(db.importRelationships(tuples(4))).rejects.toBeInstanceOf(UnavailableError);
        expect(db.relationships).toEqual([]);
    });

    it("fails an export after K items, and a read stream before any", async () => {
        const db = new InMemorySpiceDB(tuples(5));
        db.setFaultHook(fault => fault.call.operation === "exportRelationships" && fault.stage === FaultStage.Item && fault.item === 3
            ? InMemorySpiceDB.serverError(Code.Unavailable, "stream reset") : undefined);
        const got: Relationship[] = [];
        const error = await (async () => { for await (const rel of db.exportRelationships({ consistency: full() })) got.push(rel); })().catch((e: unknown) => e);
        expect(got).toHaveLength(3);
        expect(error).toBeInstanceOf(UnavailableError);

        db.failNext("readRelationships", InMemorySpiceDB.serverError(Code.Unavailable, "down"));
        const stream = db.readRelationships({ filter: { resourceType: "document" }, consistency: full() });
        await expect(drain(stream)).rejects.toBeInstanceOf(UnavailableError);
        expect(await drain(db.readRelationships({ filter: { resourceType: "document" }, consistency: full() }))).toHaveLength(5);
    });

    it("fails exactly the next call of one operation, and a failed write applies nothing", async () => {
        const db = new InMemorySpiceDB();
        const check = { resource: { objectType: "document", objectId: "d0" }, permission: "view", subject: { object: { objectType: "user", objectId: "x" } }, consistency: full() };
        db.failNext("check", InMemorySpiceDB.serverError(Code.Unavailable, "down"));
        await db.readSchema().catch(() => undefined); // another operation is unaffected by the fault
        await expect(db.check(check)).rejects.toBeInstanceOf(UnavailableError);
        expect((await db.check(check)).permissionship).toBe("noPermission");

        db.failNext("write", InMemorySpiceDB.serverError(Code.Unavailable, "down"));
        await expect(db.write(new Transaction().touch(docViewer("a", "b")))).rejects.toBeInstanceOf(UnavailableError);
        expect(db.relationships).toEqual([]);
        await db.write(new Transaction().touch(docViewer("a", "b")));
        expect(db.relationships).toHaveLength(1);
    });
});

describe("InMemorySpiceDB call recording", () => {
    it("records each call with its operation, index, request and options", async () => {
        const db = new InMemorySpiceDB(tuples(3));
        const signal = new AbortController().signal;
        await drain(db.readRelationships({ filter: { resourceType: "document" }, consistency: full(), cursor: "1", limit: 1 }, { timeoutMs: 500, signal }));
        await drain(db.readRelationships({ filter: { resourceType: "folder" }, consistency: full() }));
        await db.deleteAllRelationshipsNonAtomic({ resourceType: "document" }, { pageSize: 2 });
        await db.checkBulk([]);

        const reads = db.callsOf("readRelationships");
        expect(reads.map(c => c.index)).toEqual([0, 1]);
        expect(reads[0]!.request.cursor).toBe("1");
        expect(reads[0]!.request.limit).toBe(1);
        expect(reads[0]!.options?.timeoutMs).toBe(500);
        expect(reads[0]!.options?.signal).toBe(signal);
        expect(reads[1]!.request.filter.resourceType).toBe("folder");
        expect(reads[1]!.options).toBeUndefined();
        // Composite operations record one call, not their internal pages or items.
        expect(db.calls.map(c => c.operation)).toEqual(["readRelationships", "readRelationships", "deleteAllRelationshipsNonAtomic", "checkBulk"]);
        expect(db.callsOf("deleteAllRelationshipsNonAtomic")[0]!.options?.pageSize).toBe(2);
        expect(Object.isFrozen(db.calls[0])).toBe(true);
    });
});
