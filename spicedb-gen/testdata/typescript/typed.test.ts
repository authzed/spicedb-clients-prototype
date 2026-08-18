import { describe, it, expect, beforeAll } from "vitest";
import { full, type Relationship } from "@spicedb/client";
import { TypedClient, Document, User, Team } from "./permissions";

const SCHEMA = `
caveat ip_range(allowed_cidr string) {
    allowed_cidr == "0.0.0.0/0"
}
caveat time_window(start string, end string) {
    start != "" && end != ""
}
definition user {}
definition team {
    relation member: user | team#member
}
definition document {
    relation viewer: user | user with ip_range | user with time_window | team#member
    relation editor: user
    relation owner: user
    permission view = viewer + editor + owner
    permission edit = editor + owner
    permission delete = owner
}`;

describe("TypedClient", () => {
    let tc: TypedClient;

    beforeAll(async () => {
        tc = TypedClient.create("localhost:50051", "somerandomkeyhere", { insecure: true });
        await tc.client.writeSchema(SCHEMA);
    });

    describe("touch + check", () => {
        it("writes relationships and checks permissions", async () => {
            await tc.touch(
                Document("readme").viewer(User("alice")),
                Document("readme").editor(User("bob")),
                Document("readme").owner(User("charlie")),
                Document("readme").viewer(Team("eng").member()),
            );

            expect((await tc.check(full(), Document("readme").view, User("alice"))).hasPermission()).toBe(true);
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
            await tc.touch(
                Document("readme").viewer(User("frank").withIpRange({})),
            );

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
            await tc.touch(
                Document("readme").viewer(User("grace").withIpRange({})),
            );

            const result = await tc.check(
                full(), Document("readme").view, User("grace").withIpRange({ allowedCidr: "0.0.0.0/0" }),
            );
            expect(result.hasPermission()).toBe(true);
        });

        it("merges: subject's own context wins over a conflicting call-level default", async () => {
            // The call-level default is a WRONG cidr that would fail the caveat
            // on its own; the subject's own embedded context supplies the
            // CORRECT cidr, which must win per-key.
            await tc.touch(
                Document("readme").viewer(User("henry").withIpRange({})),
            );

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
            await tc.touch(
                Document("readme").viewer(User("ivan").withTimeWindow({})),
            );

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
            for await (const res of await tc.lookupResources(full(), Document.view, User("alice"))) {
                ids.push(res.resourceId);
            }
            expect(ids).toContain("readme");
        });
    });

    describe("lookupSubjects", () => {
        it("finds users who can view a document", async () => {
            const ids: string[] = [];
            for await (const sub of await tc.lookupSubjects(full(), Document("readme").view, User)) {
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
});
