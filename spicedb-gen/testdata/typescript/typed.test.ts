import { describe, it, expect, beforeAll } from "vitest";
import { full, type Relationship } from "@spicedb/client";
import { TypedClient, Document, User, Team } from "./permissions";

const SCHEMA = `
definition user {}
definition team {
    relation member: user | team#member
}
definition document {
    relation viewer: user | team#member
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

            expect(await tc.check(full(), Document("readme").view, User("alice"))).toBe(true);
            expect(await tc.check(full(), Document("readme").edit, User("alice"))).toBe(false);
            expect(await tc.check(full(), Document("readme").view, User("bob"))).toBe(true);
            expect(await tc.check(full(), Document("readme").edit, User("bob"))).toBe(true);
            expect(await tc.check(full(), Document("readme").delete, User("charlie"))).toBe(true);
            expect(await tc.check(full(), Document("readme").view, Team("eng").member())).toBe(true);
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
