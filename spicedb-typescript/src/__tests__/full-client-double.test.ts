// The full client interface, the test double's coverage of it, and the
// double's schema-revision, import-batch and cursor helpers (root DESIGN.md:
// "Make native values observable and testable").
import { describe, it, expect } from "vitest";
import { Code, createRouterTransport } from "@connectrpc/connect";
import {
    SpiceDBClient, InMemorySpiceDB, FaultStage, SchemaRevision, SpiceDBError, AlreadyExistsError, InvalidArgumentError, full, relationship, errorCode,
    type SpiceDBClientInterface, type Relationship,
} from "../index.js";

// Compile-time: the interface lists exactly the client's public methods, with
// the same signatures, and both the client and the double implement it.
type Equal<A, B> = (<T>() => T extends A ? 1 : 2) extends (<T>() => T extends B ? 1 : 2) ? true : false;
type PublicMethods<T> = { [K in keyof T]: T[K] };
const sameKeys: Equal<keyof SpiceDBClient, keyof SpiceDBClientInterface> = true;
const sameSignatures: Equal<PublicMethods<SpiceDBClient>, PublicMethods<SpiceDBClientInterface>> = true;
const clientImplements = (client: SpiceDBClient): SpiceDBClientInterface => client;
const doubleImplements = (db: InMemorySpiceDB): SpiceDBClientInterface => db;
void clientImplements; void doubleImplements;

// Runtime: every interface method (the record must name each key to compile).
const interfaceMethods: Record<keyof SpiceDBClientInterface, true> = {
    raw: true, close: true, ready: true, check: true, checkBulk: true, write: true, readRelationships: true, deleteRelationships: true,
    deleteAllRelationshipsNonAtomic: true, lookupResources: true, lookupSubjects: true, expandPermissionTree: true,
    importRelationships: true, exportRelationships: true, readSchema: true, writeSchema: true, reflectSchema: true,
    computablePermissions: true, dependentRelations: true, diffSchema: true, watch: true,
    experimentalRegisterRelationshipCounter: true, experimentalCountRelationships: true, experimentalUnregisterRelationshipCounter: true,
    experimentalCountRelationshipsByFilter: true, experimentalWatchPermissionSets: true, experimentalLookupPermissionSets: true,
    experimentalDownloadPermissionSets: true, experimentalRoaringLookupResources: true,
};

const drain = async <T>(it: AsyncIterable<T>) => { const out: T[] = []; for await (const x of it) out.push(x); return out; };
const doc = (n: number) => relationship(`document:d${n}`, "viewer", "user:x");
const docs = (n: number) => Array.from({ length: n }, (_, i) => doc(i));

describe("SpiceDBClientInterface", () => {
    it("matches the client's public methods, and both implementations define them", () => {
        expect(sameKeys && sameSignatures).toBe(true);
        for (const name of Object.keys(interfaceMethods)) {
            expect(typeof (SpiceDBClient.prototype as unknown as Record<string, unknown>)[name], name).toBe("function");
            expect(typeof (InMemorySpiceDB.prototype as unknown as Record<string, unknown>)[name], name).toBe("function");
        }
    });
});

describe("InMemorySpiceDB covers the whole client", () => {
    it("records operations without in-memory semantics and fails them as the server would", async () => {
        const db = new InMemorySpiceDB();
        const error = await db.expandPermissionTree({ resource: { objectType: "doc", objectId: "a" }, permission: "view", consistency: full() }).catch((e: unknown) => e);
        expect(error).toBeInstanceOf(SpiceDBError);
        expect(errorCode(error)).toBe(Code.Unimplemented);
        expect((error as SpiceDBError).message).toBe("[unimplemented] expandPermissionTree is not implemented by InMemorySpiceDB");
        expect((error as SpiceDBError).serverMessage).toBe("expandPermissionTree is not implemented by InMemorySpiceDB");

        // A stream fails from iteration, and the call is still recorded.
        const stream = db.watch();
        await expect(drain(stream)).rejects.toMatchObject({ code: Code.Unimplemented });
        expect(db.callsOf("watch")).toHaveLength(1);
        await expect(db.experimentalRegisterRelationshipCounter("c", { resourceType: "doc" })).rejects.toMatchObject({ code: Code.Unimplemented });
        expect(db.callsOf("experimentalRegisterRelationshipCounter")[0]!.request).toEqual({ name: "c", filter: { resourceType: "doc" } });
        expect(() => db.raw()).toThrow(SpiceDBError);
    });

    it("has the same error class and code as a real client talking to a server without the operation", async () => {
        const client = SpiceDBClient.fromTransport(createRouterTransport(() => {}), { retryPolicy: { maxRetries: 0, retryableCodes: [], initialBackoffMs: 1 } });
        const real = await client.expandPermissionTree({ resource: { objectType: "document", objectId: "d" }, permission: "view", consistency: full() }).catch((e: unknown) => e);
        const fake = await new InMemorySpiceDB().expandPermissionTree({ resource: { objectType: "document", objectId: "d" }, permission: "view", consistency: full() }).catch((e: unknown) => e);
        expect(fake).toBeInstanceOf((real as object).constructor);
        expect(errorCode(fake)).toBe(errorCode(real));
        expect((real as SpiceDBError).message.startsWith("[unimplemented] ")).toBe(true);
        expect((fake as SpiceDBError).message.startsWith("[unimplemented] ")).toBe(true);
        client.close();
    });

    it("consults the fault hook for unimplemented operations", async () => {
        const db = new InMemorySpiceDB();
        db.failNext("lookupResources", InMemorySpiceDB.serverError(Code.Unavailable, "down"));
        await expect(drain(db.lookupResources({ resourceType: "doc", permission: "view", subject: { object: { objectType: "user", objectId: "x" } }, consistency: full() })))
            .rejects.toMatchObject({ code: Code.Unavailable });
        db.failNext("experimentalCountRelationships", InMemorySpiceDB.serverError(Code.Unavailable, "down"));
        await expect(db.experimentalCountRelationships("c")).rejects.toMatchObject({ code: Code.Unavailable });
    });

    it("rejects a retry opt-in on never-idempotent counter mutations, as the client does", async () => {
        const db = new InMemorySpiceDB();
        await expect(db.experimentalUnregisterRelationshipCounter("c", { retryPolicy: { maxRetries: 2, retryableCodes: [Code.Unavailable], initialBackoffMs: 1 } }))
            .rejects.toBeInstanceOf(InvalidArgumentError);
    });
});

describe("InMemorySpiceDB schema reads without a revision", () => {
    it("reports an empty revision under SchemaRevision.Omitted", async () => {
        const db = new InMemorySpiceDB();
        await db.writeSchema("definition user {}");
        expect((await db.readSchema()).revision.toString()).not.toBe("");
        db.setReadSchemaRevision(SchemaRevision.Omitted);
        const omitted = await db.readSchema();
        expect(omitted.revision.toString()).toBe("");
        expect(omitted.schema).toBe("definition user {}");
        db.setReadSchemaRevision(SchemaRevision.Included);
        expect((await db.readSchema()).revision.toString()).not.toBe("");
    });
});

describe("InMemorySpiceDB import-batch faults", () => {
    it("carry the batch's relationships", async () => {
        const db = new InMemorySpiceDB();
        const batches: string[][] = [];
        db.setFaultHook(fault => {
            if (fault.stage !== FaultStage.ImportBatch) {
                expect(fault.batch).toBeUndefined();
                return undefined;
            }
            const ids = fault.batch!.map((rel: Relationship) => rel.resource.objectId);
            if (ids.includes("d4"))
                return InMemorySpiceDB.serverError(Code.AlreadyExists, "duplicate d4");
            batches.push(ids);
            return undefined;
        });
        await expect(db.importRelationships(docs(7), { batchSize: 3 })).rejects.toBeInstanceOf(AlreadyExistsError);
        expect(batches).toEqual([["d0", "d1", "d2"]]);
        expect(db.relationships).toEqual([]);

        // The final partial batch is delivered too.
        const seen: [number, number][] = [];
        db.setFaultHook(fault => { if (fault.stage === FaultStage.ImportBatch) seen.push([fault.item, fault.batch!.length]); return undefined; });
        await db.importRelationships(docs(7), { batchSize: 3 });
        expect(seen).toEqual([[0, 3], [1, 3], [2, 1]]);
    });
});

describe("InMemorySpiceDB.cursorAfter", () => {
    it("equals the cursor on the n-th item and resumes after it", async () => {
        const db = new InMemorySpiceDB(docs(5));
        const cursors = (await drain(db.exportRelationships({ consistency: full() }))).map(rel => rel.afterResultCursor);
        expect(cursors[1]).toBe(InMemorySpiceDB.cursorAfter(2));
        const rest = await drain(db.exportRelationships({ consistency: full(), cursor: InMemorySpiceDB.cursorAfter(3) }));
        expect(rest.map(rel => rel.resource.objectId)).toEqual(["d3", "d4"]);
        const read = await drain(db.readRelationships({ filter: { resourceType: "document" }, consistency: full(), cursor: InMemorySpiceDB.cursorAfter(4) }));
        expect(read.map(rel => rel.resource.objectId)).toEqual(["d4"]);
        expect(() => InMemorySpiceDB.cursorAfter(-1)).toThrow(InvalidArgumentError);
    });
});
