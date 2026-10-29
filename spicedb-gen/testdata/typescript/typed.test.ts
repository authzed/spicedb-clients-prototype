import { readFileSync } from "node:fs";
import { LookupPermissionshipFilter, Tracing, TransportSecurity, RemoteCredentials, ZedToken, InvalidArgumentError } from "@spicedb/client";
import { describe, it, expect, beforeAll, afterAll } from "vitest";
import { full, type Relationship } from "@spicedb/client";
import { TypedClient, Document, User, Team, Organization, definitionByName, subjectSetByName } from "./permissions";

// The full sample schema, shared with adoption.test.ts: test files run in
// parallel against one SpiceDB, so they must all write the same schema.
const SCHEMA = readFileSync(new URL("../sample.zed", import.meta.url), "utf8");

describe("TypedClient", () => {
    let tc: TypedClient;

    beforeAll(async () => {
        tc = TypedClient.create(process.env.SPICEDB_ENDPOINT ?? "localhost:50051", process.env.SPICEDB_TOKEN ?? "somerandomkeyhere", { transportSecurity: TransportSecurity.Plaintext });
        await tc.writeSchema(SCHEMA);
    });

    afterAll(() => tc.close());

    it("exposes bounded and non-atomic filter deletion directly", async () => {
        await tc.touch(["a", "b", "c"].map(id => Document("parity-delete").viewer(User(id))));
        const filter = { resourceType: "document", resourceId: "parity-delete" };
        const first = await tc.deleteRelationships(filter, { limit: 1 });
        expect(first.deletedCount).toBe(1n);
        const remaining = await tc.deleteAllRelationshipsNonAtomic(filter, { pageSize: 1 });
        expect(remaining.deletedCount).toBe(2n);
        expect(remaining.progress).toBe("complete");
        expect(remaining.revision).toBeInstanceOf(ZedToken);
    });

    describe("touch + check", () => {
        it("writes relationships and checks permissions", async () => {
            await tc.touch([
                Document("readme").viewer(User("alice")),
                Document("readme").editor(User("bob")),
                Document("readme").owner(User("charlie")),
                Document("readme").viewer(Team("eng").member()),
            ]);

            const traced = await tc.check(full(), Document("readme").view, User("alice"), { tracing: Tracing.Enabled });
            expect(traced.hasPermission()).toBe(true);
            expect(traced.checkedAt).toBeInstanceOf(ZedToken);
            expect(traced.checkedAt.toString()).not.toBe("");
            expect(traced.debugTrace?.schemaUsed).toContain("definition document");
            expect(traced.debugTrace?.check).toBeDefined();
            expect((await tc.check(full(), Document("readme").edit, User("alice"))).hasPermission()).toBe(false);
            expect((await tc.check(full(), Document("readme").view, User("bob"))).hasPermission()).toBe(true);
            expect((await tc.check(full(), Document("readme").edit, User("bob"))).hasPermission()).toBe(true);
            expect((await tc.check(full(), Document("readme").delete, User("charlie"))).hasPermission()).toBe(true);
            expect((await tc.check(full(), Document("readme").view, Team("eng").member())).hasPermission()).toBe(true);
        });

        it("surfaces a caveated relationship missing context as conditional, not a bare denial", async () => {
            // This is the state a boolean return would have collapsed away --
            // it's reachable here only because tc.check() returns the full
            // CheckResult instead of a bool. Before Task 4/11, TypeScript's
            // check() would have returned `true` for this case: a fail-open bug.
            await tc.touch([
                Document("readme").viewer(User("frank").withIpRange({})),
            ]);

            const result = await tc.check(full(), Document("readme").view, User("frank"));
            expect(result.hasPermission()).toBe(false);
            expect(result.permissionship).toBe("conditionalPermission");
            expect(result.missingContext).toContain("allowed_cidr");
            expect(result.checkedAt).toBeTruthy();
        });

        it("resolves a conditional check into a grant when context is supplied at check time", async () => {
            // The payoff (spec D3b): frank's relationship (touched above) is still
            // missing allowed_cidr at write time; supplying it via the new
            // `options.context` parameter at CHECK time must resolve the caveat
            // into a grant.
            const resolved = await tc.check(full(), Document("readme").view, User("frank"), {
                context: { allowed_cidr: "0.0.0.0/0" },
            });
            expect(resolved.hasPermission()).toBe(true);
            expect(resolved.permissionship).toBe("hasPermission");
        });

        it("resolves a conditional check via the subject's own embedded context alone (no call-level options)", async () => {
            // A caveated relationship with no context supplied at write time. The
            // PLAIN check() call (no options/call-level context at all) must
            // still resolve it via the subject's OWN embedded context
            // (User(...).withIpRange(ctx) passed directly as the check subject) --
            // this is what makes the fix functional for the simplest call shape.
            await tc.touch([
                Document("readme").viewer(User("grace").withIpRange({})),
            ]);

            const result = await tc.check(
                full(), Document("readme").view, User("grace").withIpRange({ allowedCidr: "0.0.0.0/0" }),
            );
            expect(result.hasPermission()).toBe(true);
        });

        it("merges: subject's own context wins over a conflicting call-level default", async () => {
            // The call-level default is a WRONG cidr that would fail the caveat
            // on its own; the subject's own embedded context supplies the
            // CORRECT cidr, which must win per-key.
            await tc.touch([
                Document("readme").viewer(User("henry").withIpRange({})),
            ]);

            const result = await tc.check(
                full(), Document("readme").view,
                User("henry").withIpRange({ allowedCidr: "0.0.0.0/0" }),
                { context: { allowed_cidr: "wrong-value-must-be-overridden" } },
            );
            expect(result.hasPermission()).toBe(true);
        });

        it("merges: a call-level key the subject doesn't mention survives (not wholesale replacement)", async () => {
            // The subject supplies ONLY "start" (via withTimeWindow); the
            // call-level default supplies "end", a key the subject never
            // mentions. If the merge were wholesale replacement instead of a
            // key-level merge, "end" would be silently dropped and the caveat
            // (start != "" && end != "") would come back conditional on a
            // MISSING "end", not a grant.
            await tc.touch([
                Document("readme").viewer(User("ivan").withTimeWindow({})),
            ]);

            const result = await tc.check(
                full(), Document("readme").view,
                User("ivan").withTimeWindow({ start: "9am" }),
                { context: { end: "5pm" } },
            );
            expect(result.hasPermission()).toBe(true);
            expect(result.missingContext).toEqual([]);
        });
    });

    describe("lookupResources", () => {
        it("finds resources a user can view", async () => {
            const ids: string[] = [];
            for await (const res of tc.lookupResources(full(), Document.view, User("alice"), { permissionshipFilter: LookupPermissionshipFilter.HasPermissionOnly })) {
                ids.push(res.resourceId);
            }
            expect(ids).toContain("readme");
        });
    });

    describe("lookupSubjects", () => {
        it("finds users who can view a document", async () => {
            const ids: string[] = [];
            for await (const sub of tc.lookupSubjects(full(), Document("readme").view, User)) {
                if (sub.subject.permissionship !== "hasPermission" || sub.subject.subjectId === "*") continue;
                ids.push(sub.subject.subjectId);
            }
            expect(ids).toContain("alice");
            expect(ids).toContain("bob");
            expect(ids).toContain("charlie");
        });
    });

    describe("readRelationships", () => {
        it("reads relationships matching a filter", async () => {
            const rels: Relationship[] = [];
            for await (const rel of tc.readRelationships(full(), { _type: "document" })) {
                rels.push(rel);
            }
            expect(rels.length).toBeGreaterThan(0);
        });
    });

    describe("runtime names", () => {
        it("writes, checks and looks up by name against the server", async () => {
            const eng = subjectSetByName("team", "rn-eng", "member");
            await tc.touch([
                definitionByName("document").ref("rn-doc").relationshipByName("viewer", eng),
                definitionByName("team").ref("rn-eng").relationshipByName("member", User("rn-alice")),
            ]);
            expect((await tc.checkByName(full(), "document", "rn-doc", "view", User("rn-alice"))).hasPermission()).toBe(true);
            expect((await tc.checkByName(full(), "document", "rn-doc", "viewer", eng)).hasPermission()).toBe(true);

            const docs: string[] = [];
            for await (const r of tc.lookupResourcesByName(full(), "document", "viewer", eng)) docs.push(r.resourceId);
            expect(docs).toEqual(["rn-doc"]);

            const sets: string[] = [];
            for await (const s of tc.lookupSubjectsByName(full(), "document", "rn-doc", "viewer", "team#member")) sets.push(s.subject.subjectId);
            expect(sets).toEqual(["rn-eng"]);
            const users: string[] = [];
            for await (const s of tc.lookupSubjectsByName(full(), "document", "rn-doc", "view", "user")) users.push(s.subject.subjectId);
            expect(users).toEqual(["rn-alice"]);

            const none: string[] = [];
            for await (const s of tc.lookupSubjectsByName(full(), "document", "rn-doc", "view", "organization")) none.push(s.subject.subjectId);
            for await (const r of tc.lookupResourcesByName(full(), "document", "view", Organization("rn-org"))) none.push(r.resourceId);
            expect(none).toEqual([]);
        });
    });
});

describe("typed connection policies", () => {
    it("forwards explicit remote plaintext policy through the normal factory", () => {
        expect(() => TypedClient.create("remote.example.invalid:50051", "secret", { transportSecurity: TransportSecurity.Plaintext })).toThrow(InvalidArgumentError);
        const allowed = TypedClient.create("remote.example.invalid:50051", "secret", {
            transportSecurity: TransportSecurity.Plaintext, remoteCredentials: RemoteCredentials.AllowPlaintext,
        });
        allowed.close();
    });
});
