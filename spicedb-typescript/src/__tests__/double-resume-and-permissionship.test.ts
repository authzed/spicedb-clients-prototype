// The double's injected stream faults carry `resumeCursor` as the real
// client's errors do, an unset permissionship is a client-detected
// malformed response (root DESIGN.md: "A response the client cannot
// interpret ... fails with INTERNAL ... The error says the client detected
// it"), and the double never produces one.
import { describe, it, expect } from "vitest";
import { create } from "@bufbuild/protobuf";
import { Code, createRouterTransport } from "@connectrpc/connect";
import {
    PermissionsService, CheckPermissionResponseSchema, CheckPermissionResponse_Permissionship, CheckBulkPermissionsResponseSchema,
    CheckBulkPermissionsPairSchema, CheckBulkPermissionsResponseItemSchema, LookupResourcesResponseSchema, LookupSubjectsResponseSchema,
    ResolvedSubjectSchema, LookupPermissionship,
} from "@spicedb/proto";
import {
    SpiceDBClient, InMemorySpiceDB, FaultStage, Retries, SpiceDBError, InvalidArgumentError, BulkItemErrors, LookupPermissionshipFilter,
    errorCode, full, relationship, type CheckRequest,
} from "../index.js";

const subject = { object: { objectType: "user", objectId: "alice" } };
const checkReq: CheckRequest = { resource: { objectType: "document", objectId: "a" }, permission: "view", subject, consistency: full() };
const lookupReq = { resourceType: "document", permission: "viewer", subject, consistency: full() };
const docs = (n: number) => Array.from({ length: n }, (_, i) => relationship(`document:d${i}`, "viewer", "user:alice"));

async function failure(iterable: AsyncIterable<{ afterResultCursor?: string }>): Promise<{ error: unknown; cursors: (string | undefined)[] }> {
    const cursors: (string | undefined)[] = [];
    try {
        for await (const item of iterable)
            cursors.push(item.afterResultCursor);
    }
    catch (error) {
        return { error, cursors };
    }
    throw new Error("expected the read to fail");
}
const failAtItem = (db: InMemorySpiceDB, k: number) =>
    db.setFaultHook(f => f.stage === FaultStage.Item && f.item === k ? InMemorySpiceDB.serverError(Code.Unavailable, "stream reset") : undefined);

describe("InMemorySpiceDB faults on resumable reads carry resumeCursor", () => {
    const reads = {
        readRelationships: (db: InMemorySpiceDB, cursor?: string) => db.readRelationships({ filter: { resourceType: "document" }, consistency: full(), ...(cursor === undefined ? {} : { cursor }) }),
        exportRelationships: (db: InMemorySpiceDB, cursor?: string) => db.exportRelationships({ consistency: full(), ...(cursor === undefined ? {} : { cursor }) }),
        lookupResources: (db: InMemorySpiceDB, cursor?: string) => db.lookupResources({ ...lookupReq, ...(cursor === undefined ? {} : { cursor }) }),
    };
    it.each(Object.entries(reads))("%s: a fault partway through reports the last delivered item's cursor, and resuming from it finishes the read", async (_, read) => {
        const db = new InMemorySpiceDB(docs(5));
        const all = (await failure((async function* () { yield* read(db); throw new Error("end"); })())).cursors;
        failAtItem(db, 3);
        const { error, cursors } = await failure(read(db));
        expect(cursors).toEqual(all.slice(0, 3));
        expect(error).toBeInstanceOf(SpiceDBError);
        expect((error as SpiceDBError).resumeCursor).toBe(cursors[2]);
        db.setFaultHook(undefined);
        const rest: (string | undefined)[] = [];
        for await (const item of read(db, (error as SpiceDBError).resumeCursor))
            rest.push(item.afterResultCursor);
        expect([...cursors, ...rest]).toEqual(all);
    });
    it.each(Object.entries(reads))("%s: a fault before any item reports the request's cursor, or none", async (_, read) => {
        const db = new InMemorySpiceDB(docs(5));
        failAtItem(db, 0);
        expect(((await failure(read(db))).error as SpiceDBError).resumeCursor).toBeUndefined();
        expect(((await failure(read(db, "2"))).error as SpiceDBError).resumeCursor).toBe("2");
        db.setFaultHook(f => f.stage === FaultStage.Call ? InMemorySpiceDB.serverError(Code.Unavailable, "down") : undefined);
        expect(((await failure(read(db, "1"))).error as SpiceDBError).resumeCursor).toBe("1");
        expect(((await failure(read(db))).error as SpiceDBError).resumeCursor).toBeUndefined();
    });
    it("clears a stale resumeCursor when the same injected error is reused", async () => {
        const db = new InMemorySpiceDB(docs(3));
        const injected = InMemorySpiceDB.serverError(Code.Unavailable, "down");
        db.setFaultHook(f => f.stage === FaultStage.Item && f.item === (f.call.index === 0 ? 2 : 0) ? injected : undefined);
        expect(((await failure(reads.readRelationships(db))).error as SpiceDBError).resumeCursor).toBe("2");
        expect(((await failure(reads.readRelationships(db))).error as SpiceDBError).resumeCursor).toBeUndefined();
    });
    it("lookupSubjects is not resumable and reports no cursor", async () => {
        const db = new InMemorySpiceDB([relationship("document:d", "viewer", "user:a"), relationship("document:d", "viewer", "user:b")]);
        failAtItem(db, 1);
        let error: unknown;
        try {
            for await (const _ of db.lookupSubjects({ resource: { objectType: "document", objectId: "d" }, permission: "viewer", subjectType: "user", consistency: full() }))
                void _;
        }
        catch (e) {
            error = e;
        }
        expect((error as SpiceDBError).resumeCursor).toBeUndefined();
    });
});

describe("an unset permissionship is a client-detected malformed response", () => {
    const clientFor = (routes: Parameters<typeof createRouterTransport>[0]) => SpiceDBClient.fromTransport(createRouterTransport(routes), { retries: Retries.Disabled });
    const expectMalformed = (error: unknown, detail: RegExp) => {
        expect(error).toBeInstanceOf(SpiceDBError);
        expect(errorCode(error)).toBe(Code.Internal);
        expect((error as Error).message).toMatch(/^client detected malformed response from server: /);
        expect((error as Error).message).toMatch(detail);
        expect((error as SpiceDBError).serverMessage).toBeUndefined();
    };
    it("fails a check", async () => {
        const client = clientFor(router => router.service(PermissionsService, {
            async checkPermission() { return create(CheckPermissionResponseSchema, { permissionship: CheckPermissionResponse_Permissionship.UNSPECIFIED }); },
        }));
        expectMalformed(await client.check(checkReq).catch((e: unknown) => e), /check response: permissionship is unset/);
        client.close();
    });
    it("fails a bulk-check item, naming its index, even when item errors are returned", async () => {
        const client = clientFor(router => router.service(PermissionsService, {
            async checkBulkPermissions(req) {
                return create(CheckBulkPermissionsResponseSchema, { pairs: req.items.map((item, i) => create(CheckBulkPermissionsPairSchema, { request: item,
                    response: { case: "item", value: create(CheckBulkPermissionsResponseItemSchema, { permissionship: i === 1 ? CheckPermissionResponse_Permissionship.UNSPECIFIED : CheckPermissionResponse_Permissionship.HAS_PERMISSION }) } })) });
            },
        }));
        for (const itemErrors of [BulkItemErrors.Fail, BulkItemErrors.Returned]) {
            const error = await client.checkBulk([checkReq, checkReq], { itemErrors }).catch((e: unknown) => e);
            expectMalformed(error, /check item 1: permissionship is unset/);
            expect((error as SpiceDBError).inputIndex).toBe(1);
        }
        client.close();
    });
    it.each([LookupPermissionshipFilter.All, LookupPermissionshipFilter.HasPermissionOnly])("fails a lookup-resources result instead of skipping it (filter %s)", async (permissionshipFilter) => {
        const client = clientFor(router => router.service(PermissionsService, {
            async *lookupResources() {
                yield create(LookupResourcesResponseSchema, { resourceObjectId: "a", permissionship: LookupPermissionship.HAS_PERMISSION, afterResultCursor: { token: "1" } });
                yield create(LookupResourcesResponseSchema, { resourceObjectId: "b", afterResultCursor: { token: "2" } });
            },
        }));
        const seen: string[] = [];
        const error = await (async () => { for await (const r of client.lookupResources({ ...lookupReq, permissionshipFilter })) seen.push(r.resourceId); })().catch((e: unknown) => e);
        expectMalformed(error, /lookup resources result b: permissionship is unset/);
        expect(seen).toEqual(["a"]);
        expect((error as SpiceDBError).resumeCursor).toBe("1");
        client.close();
    });
    it("fails a lookup-subjects result", async () => {
        const client = clientFor(router => router.service(PermissionsService, {
            async *lookupSubjects() { yield create(LookupSubjectsResponseSchema, { subject: create(ResolvedSubjectSchema, { subjectObjectId: "bob" }) }); },
        }));
        const error = await (async () => { for await (const _ of client.lookupSubjects({ resource: { objectType: "document", objectId: "a" }, permission: "view", subjectType: "user", consistency: full() })) void _; })().catch((e: unknown) => e);
        expectMalformed(error, /lookup subjects result bob: permissionship is unset/);
        client.close();
    });
    it("other malformed responses also say the client detected them", async () => {
        const client = clientFor(router => router.service(PermissionsService, {
            async *lookupResources() { yield create(LookupResourcesResponseSchema, { permissionship: LookupPermissionship.HAS_PERMISSION }); },
        }));
        const error = await (async () => { for await (const _ of client.lookupResources(lookupReq)) void _; })().catch((e: unknown) => e);
        expectMalformed(error, /no resource ID/);
        client.close();
    });
});

describe("InMemorySpiceDB never produces an unset permissionship", () => {
    it("rejects registering one", () => {
        const db = new InMemorySpiceDB();
        expect(() => db.setCheckResult(checkReq, "unspecified")).toThrow(InvalidArgumentError);
    });
    it("rejects a resolver that returns one, rather than answering with it", async () => {
        const db = new InMemorySpiceDB([relationship("document:a", "view", "user:alice")]);
        db.setCheckResolver(() => "unspecified");
        await expect(db.check(checkReq)).rejects.toBeInstanceOf(InvalidArgumentError);
        await expect((async () => { for await (const _ of db.lookupResources({ ...lookupReq, permission: "view" })) void _; })()).rejects.toBeInstanceOf(InvalidArgumentError);
        await expect((async () => { for await (const _ of db.lookupSubjects({ resource: { objectType: "document", objectId: "a" }, permission: "view", subjectType: "user", consistency: full() })) void _; })()).rejects.toBeInstanceOf(InvalidArgumentError);
    });
    it("answers every check and lookup with a specified permissionship", async () => {
        const db = new InMemorySpiceDB(docs(3));
        expect((await db.check(checkReq)).permissionship).toBe("noPermission");
        const results = await db.checkBulk([checkReq, { ...checkReq, resource: { objectType: "document", objectId: "d0" }, permission: "viewer" }]);
        expect(results.map(r => r.permissionship)).toEqual(["noPermission", "hasPermission"]);
        for await (const r of db.lookupResources(lookupReq))
            expect(r.permissionship).not.toBe("unspecified");
        for await (const r of db.lookupSubjects({ resource: { objectType: "document", objectId: "d0" }, permission: "viewer", subjectType: "user", consistency: full() }))
            expect(r.subject.permissionship).toBe("hasPermission");
    });
});
