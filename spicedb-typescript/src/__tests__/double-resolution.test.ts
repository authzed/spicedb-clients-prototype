// InMemorySpiceDB check resolution and lookups (root DESIGN.md: the double
// resolves a check in a fixed order -- registered result, check resolver,
// stored relationships, otherwise no permission -- and serves LookupResources
// and LookupSubjects from its known objects through that resolution).
import { describe, it, expect } from "vitest";
import { Code } from "@connectrpc/connect";
import {
    InMemorySpiceDB, FaultStage, LookupPermissionshipFilter, BulkItemErrors, Transaction, Wildcards, NotFoundError, InvalidArgumentError, full, relationship, relationshipFromTuple,
    type CheckRequest, type SubjectRef,
} from "../index.js";

const drain = async <T>(it: AsyncIterable<T>) => { const out: T[] = []; for await (const x of it) out.push(x); return out; };
const user = (id: string, relation?: string): SubjectRef => ({ object: { objectType: "user", objectId: id }, ...(relation === undefined ? {} : { relation }) });
const check = (doc: string, permission: string, subject: SubjectRef): CheckRequest => ({ resource: { objectType: "document", objectId: doc }, permission, subject, consistency: full() });
const permissionOf = async (db: InMemorySpiceDB, req: CheckRequest) => (await db.check(req)).permissionship;

describe("InMemorySpiceDB check resolution order", () => {
    it("grants from a stored relationship whose relation is the permission's name", async () => {
        const db = new InMemorySpiceDB([relationship("document:d1", "viewer", "user:alice")]);
        expect(await permissionOf(db, check("d1", "viewer", user("alice")))).toBe("hasPermission");
        expect(await permissionOf(db, check("d1", "editor", user("alice")))).toBe("noPermission");
        expect(await permissionOf(db, check("d1", "viewer", user("bob")))).toBe("noPermission");
        expect(await permissionOf(db, check("d2", "viewer", user("alice")))).toBe("noPermission");
    });

    it("treats a subject relation of ... as a direct subject", async () => {
        const db = new InMemorySpiceDB([relationship("document:d1", "viewer", "user:alice")]);
        expect(await permissionOf(db, check("d1", "viewer", user("alice", "...")))).toBe("hasPermission");
        db.setCheckResult(check("d1", "edit", user("alice", "...")), "hasPermission");
        expect(await permissionOf(db, check("d1", "edit", user("alice")))).toBe("hasPermission");
    });

    it("grants a direct subject through its type's stored wildcard, but not a userset", async () => {
        const db = new InMemorySpiceDB([relationship("document:d1", "viewer", "user:*")]);
        expect(await permissionOf(db, check("d1", "viewer", user("anyone")))).toBe("hasPermission");
        expect(await permissionOf(db, check("d1", "viewer", user("anyone", "member")))).toBe("noPermission");
        expect(await permissionOf(db, check("d1", "viewer", { object: { objectType: "team", objectId: "t" } }))).toBe("noPermission");
    });

    it("matches a userset subject exactly", async () => {
        const db = new InMemorySpiceDB([relationship("document:d1", "viewer", "team:eng#member")]);
        expect(await permissionOf(db, check("d1", "viewer", { object: { objectType: "team", objectId: "eng" }, relation: "member" }))).toBe("hasPermission");
        expect(await permissionOf(db, check("d1", "viewer", { object: { objectType: "team", objectId: "eng" } }))).toBe("noPermission");
    });

    it("reports a caveated stored relationship as conditional, and an uncaveated one wins", async () => {
        const db = new InMemorySpiceDB([relationshipFromTuple("document:d1#viewer@user:alice[ip_range]")]);
        expect(await permissionOf(db, check("d1", "viewer", user("alice")))).toBe("conditionalPermission");
        await db.write(new Transaction().touch(relationship("document:d1", "viewer", "user:*")));
        expect(await permissionOf(db, check("d1", "viewer", user("alice")))).toBe("hasPermission");
    });

    it("ignores expired relationships", async () => {
        const db = new InMemorySpiceDB([{ ...relationship("document:d1", "viewer", "user:alice"), expiration: new Date(Date.now() - 1000) }]);
        expect(await permissionOf(db, check("d1", "viewer", user("alice")))).toBe("noPermission");
    });

    it("consults the resolver before the stored relationships, and falls through when it returns undefined", async () => {
        const db = new InMemorySpiceDB([relationship("document:d1", "viewer", "user:alice")]);
        const seen: CheckRequest[] = [];
        db.setCheckResolver(req => {
            seen.push(req);
            return req.resource.objectId === "d1" ? "noPermission" : req.resource.objectId.startsWith("gen-") ? "hasPermission" : undefined;
        });
        expect(await permissionOf(db, check("d1", "viewer", user("alice")))).toBe("noPermission");
        expect(await permissionOf(db, check("gen-8f3a", "view", user("alice")))).toBe("hasPermission");
        expect(await permissionOf(db, check("d9", "viewer", user("alice")))).toBe("noPermission");
        expect(seen.map(r => r.resource.objectId)).toEqual(["d1", "gen-8f3a", "d9"]);
        db.setCheckResolver(undefined);
        expect(await permissionOf(db, check("d1", "viewer", user("alice")))).toBe("hasPermission");
    });

    it("prefers a registered result over the resolver", async () => {
        const db = new InMemorySpiceDB();
        db.setCheckResolver(InMemorySpiceDB.grantAll);
        db.setCheckResult(check("d1", "view", user("alice")), "noPermission");
        expect(await permissionOf(db, check("d1", "view", user("alice")))).toBe("noPermission");
        expect(await permissionOf(db, check("d2", "view", user("anyone")))).toBe("hasPermission");
    });

    it("fails a check only with a registered error, never from the resolver; bulk returns it per item", async () => {
        const db = new InMemorySpiceDB();
        db.setCheckResolver(InMemorySpiceDB.grantAll);
        db.setCheckResult(check("bad", "view", user("a")), InMemorySpiceDB.serverError(Code.NotFound, "object definition `document` not found"));
        await expect(db.check(check("bad", "view", user("a")))).rejects.toBeInstanceOf(NotFoundError);
        const results = await db.checkBulk([check("ok", "view", user("a")), check("bad", "view", user("a"))], { itemErrors: BulkItemErrors.Returned });
        expect(results.map(r => r.permissionship)).toEqual(["hasPermission", "unspecified"]);
        expect(results[1]!.error).toBeInstanceOf(NotFoundError);
    });

    it("gives the resolver the call's default context merged under the check's own", async () => {
        const db = new InMemorySpiceDB();
        const seen: unknown[] = [];
        db.setCheckResolver(req => { seen.push(req.context); return undefined; });
        await db.check({ ...check("d1", "view", user("a")), context: { ip: "own", day: "mon" } }, { context: { ip: "default", region: "eu" } });
        await db.checkBulk([check("d1", "view", user("a"))], { context: { region: "us" } });
        await db.check(check("d1", "view", user("a")));
        expect(seen).toEqual([{ ip: "own", day: "mon", region: "eu" }, { region: "us" }, undefined]);
    });
});

describe("InMemorySpiceDB lookupResources", () => {
    const store = () => new InMemorySpiceDB([
        relationship("document:d1", "viewer", "user:alice"),
        relationship("document:d2", "viewer", "user:bob"),
        relationshipFromTuple("document:d3#viewer@user:alice[ip_range]"),
        relationship("document:d4", "viewer", "user:alice"),
        relationship("folder:f1", "parent", "document:d5"),
    ]);
    const lookup = (extra: object = {}) => ({ resourceType: "document", permission: "viewer", subject: user("alice"), consistency: full(), ...extra });

    it("yields known objects the check resolution grants, in ID order, with conditional results", async () => {
        const results = await drain(store().lookupResources(lookup()));
        expect(results.map(r => [r.resourceId, r.permissionship, r.afterResultCursor])).toEqual([
            ["d1", "hasPermission", "1"], ["d3", "conditionalPermission", "2"], ["d4", "hasPermission", "3"],
        ]);
    });

    it("reports conditional results with partial-caveat info, as the real client does", async () => {
        const results = await drain(store().lookupResources(lookup()));
        expect(results[1]!.partialCaveat).toEqual({ missingRequiredContext: [] });
        expect(results[0]!.partialCaveat).toBeUndefined();
    });

    it("enumerates objects in order of first appearance among the stored relationships", async () => {
        // Identity order puts document:a's relationships before document:b's,
        // so user:zed appears before user:amy.
        const db = new InMemorySpiceDB([relationship("document:b", "viewer", "user:amy"), relationship("document:a", "viewer", "user:zed")]);
        db.setCheckResolver(InMemorySpiceDB.grantAll);
        const ids = (await drain(db.lookupSubjects({ resource: { objectType: "document", objectId: "a" }, permission: "viewer", subjectType: "user", consistency: full() }))).map(r => r.subject.subjectId);
        expect(ids).toEqual(["zed", "amy"]);
        expect(db.relationships.map(r => r.subject.object.objectId)).toEqual(["zed", "amy"]);
    });

    it("honors the permissionship filter, limit and cursor", async () => {
        const db = store();
        expect((await drain(db.lookupResources(lookup({ permissionshipFilter: LookupPermissionshipFilter.HasPermissionOnly })))).map(r => r.resourceId)).toEqual(["d1", "d4"]);
        expect((await drain(db.lookupResources(lookup({ limit: 2 })))).map(r => r.resourceId)).toEqual(["d1", "d3"]);
        expect((await drain(db.lookupResources(lookup({ cursor: InMemorySpiceDB.cursorAfter(1) })))).map(r => r.resourceId)).toEqual(["d3", "d4"]);
        // A filtered lookup's cursor resumes past the skipped conditional result.
        const first = (await drain(db.lookupResources(lookup({ permissionshipFilter: LookupPermissionshipFilter.HasPermissionOnly, limit: 1 }))))[0]!;
        expect((await drain(db.lookupResources(lookup({ cursor: first.afterResultCursor })))).map(r => r.resourceId)).toEqual(["d3", "d4"]);
    });

    it("enumerates objects that appear only as subjects, through the resolver", async () => {
        const db = store();
        db.setCheckResolver(InMemorySpiceDB.grantAll);
        expect((await drain(db.lookupResources(lookup()))).map(r => r.resourceId)).toEqual(["d1", "d2", "d3", "d4", "d5"]);
    });

    it("passes the lookup's subject, consistency and context to the resolver", async () => {
        const db = store();
        const seen: CheckRequest[] = [];
        db.setCheckResolver(req => { seen.push(req); return undefined; });
        await drain(db.lookupResources(lookup({ context: { ip: "10.0.0.1" } })));
        expect(seen).toHaveLength(5);
        expect(seen[0]).toMatchObject({ resource: { objectType: "document", objectId: "d1" }, permission: "viewer", subject: user("alice"), context: { ip: "10.0.0.1" } });
    });

    it("records the call and consults the fault hook before the call and each item", async () => {
        const db = store();
        db.setFaultHook(f => f.stage === FaultStage.Item && f.item === 1 ? InMemorySpiceDB.serverError(Code.Unavailable, "down") : undefined);
        const seen: string[] = [];
        await expect((async () => { for await (const r of db.lookupResources(lookup())) seen.push(r.resourceId); })()).rejects.toMatchObject({ code: Code.Unavailable });
        expect(seen).toEqual(["d1"]);
        expect(db.callsOf("lookupResources")).toHaveLength(1);
        expect(db.callsOf("lookupResources")[0]!.request.resourceType).toBe("document");
        db.failNext("lookupResources", InMemorySpiceDB.serverError(Code.Unavailable, "down"));
        await expect(drain(db.lookupResources(lookup()))).rejects.toMatchObject({ code: Code.Unavailable });
    });

    it("rejects invalid requests as the client does", async () => {
        const db = store();
        expect(() => db.lookupResources(lookup({ limit: 0 }))).toThrow(InvalidArgumentError);
        expect(() => db.lookupResources(lookup({ consistency: undefined }))).toThrow(InvalidArgumentError);
        await expect(drain(db.lookupResources(lookup({ cursor: "nope" })))).rejects.toMatchObject({ code: Code.InvalidArgument });
    });
});

describe("InMemorySpiceDB lookupSubjects", () => {
    const store = () => new InMemorySpiceDB([
        relationship("document:d1", "viewer", "user:alice"),
        relationship("document:d1", "viewer", "user:*"),
        relationship("document:d1", "viewer", "team:eng#member"),
        relationship("team:eng", "member", "user:carol"),
        relationship("document:d2", "viewer", "user:bob"),
    ]);
    const lookup = (extra: object = {}) => ({ resource: { objectType: "document", objectId: "d1" }, permission: "viewer", subjectType: "user", consistency: full(), ...extra });

    it("yields known subjects the check resolution grants, including the wildcard by default", async () => {
        const results = await drain(store().lookupSubjects(lookup()));
        // user:bob and user:carol are granted through user:*.
        expect(results.map(r => r.subject.subjectId)).toEqual(["*", "alice", "bob", "carol"]);
        expect(results.every(r => r.subject.permissionship === "hasPermission" && r.excludedSubjects.length === 0)).toBe(true);
    });

    it("honors wildcards, subject relation, limit and cursor", async () => {
        const db = store();
        expect((await drain(db.lookupSubjects(lookup({ wildcards: Wildcards.Exclude })))).map(r => r.subject.subjectId)).toEqual(["alice", "bob", "carol"]);
        expect((await drain(db.lookupSubjects(lookup({ subjectType: "team", subjectRelation: "member" })))).map(r => r.subject.subjectId)).toEqual(["eng"]);
        expect((await drain(db.lookupSubjects(lookup({ limit: 2 })))).map(r => r.subject.subjectId)).toEqual(["*", "alice"]);
        expect((await drain(db.lookupSubjects(lookup({ cursor: InMemorySpiceDB.cursorAfter(2) })))).map(r => r.subject.subjectId)).toEqual(["bob", "carol"]);
    });

    it("uses registered results and the resolver", async () => {
        const db = store();
        db.setCheckResult({ resource: { objectType: "document", objectId: "d1" }, permission: "viewer", subject: user("alice") }, "noPermission");
        db.setCheckResolver(req => req.subject.object.objectId === "bob" ? "conditionalPermission" : undefined);
        const results = await drain(db.lookupSubjects(lookup({ wildcards: Wildcards.Exclude })));
        expect(results.map(r => [r.subject.subjectId, r.subject.permissionship])).toEqual([["bob", "conditionalPermission"], ["carol", "hasPermission"]]);
    });

    it("records the call and consults the fault hook", async () => {
        const db = store();
        db.failNext("lookupSubjects", InMemorySpiceDB.serverError(Code.Unavailable, "down"));
        await expect(drain(db.lookupSubjects(lookup()))).rejects.toMatchObject({ code: Code.Unavailable });
        expect(db.callsOf("lookupSubjects")).toHaveLength(1);
    });
});
