// Typed-client capabilities found missing while migrating real services:
// permission usersets, observable permissions, typed transactions,
// native round trips, typed bulk checks, typed filters and
// name constants, wildcard refs, identity-only keys, relation
// checks and by-name lookups. The first group needs no server.
import { readFileSync } from "node:fs";
import { describe, it, expect, beforeAll, afterAll } from "vitest";
import { full, relationship, BulkItemErrors, FailedPreconditionError, InvalidArgumentError, TransportSecurity, filterToString, filterMatches } from "@spicedb/client";
import {
    TypedClient, TypedTransaction, Organization, Invitation, Document, User, Team, UserWildcard, OrganizationFilter, InvitationFilter,
    OrganizationRelations, OrganizationPermissions, OrganizationRelationNames, OrganizationPermissionNames, OrganizationCanAdminType,
    NotInSchemaError, SubjectNotAllowedError, fromRelationship, errRelationship, withSubject, checksFor, Permission, PermissionRef,
} from "./permissions";

describe("typed values without a server", () => {
    it("permissions and refs are observable", () => {
        const p = Organization("acme").canAdmin;
        expect(p).toBeInstanceOf(Permission);
        expect([p.resourceType, p.resourceId, p.name]).toEqual(["organization", "acme", "can_admin"]);
        expect(String(p)).toBe("organization:acme#can_admin");
        expect(p.ref().toString()).toBe("organization#can_admin");
        expect(Document.view).toBeInstanceOf(PermissionRef);
        expect(String(Document.view)).toBe("document#view");
        expect(String(Organization("acme").memberRelation)).toBe("organization:acme#member");
        expect(String(Organization("acme"))).toBe("organization:acme");
        expect(String(Team("eng").member())).toBe("team:eng#member");
        expect(JSON.stringify({ p })).toBe("{\"p\":\"organization:acme#can_admin\"}");
    });

    it("permission usersets write, print, and round-trip with native relationships", () => {
        const typed = Invitation("inv").grant(Organization("acme").canAdminSubject());
        expect(typed.toString()).toBe("invitation:inv#grant@organization:acme#can_admin");
        const native = typed.relationship();
        expect(fromRelationship(native).toString()).toBe(typed.toString());
        const broken = errRelationship(new Error("no such user"));
        expect(broken.toString()).toContain("no such user");
        expect(() => broken.relationship()).toThrow("no such user");
        expect(fromRelationship({} as never).error).toBeInstanceOf(InvalidArgumentError);
        const expiring = Invitation("inv").timedGrant(Organization("acme").canAdminSubject().withExpiration(new Date("2100-01-01T00:00:00Z")));
        expect(expiring.toString()).toBe("invitation:inv#timed_grant@organization:acme#can_admin[expiration:2100-01-01T00:00:00Z]");
    });

    it("a typed transaction defers its first error and mixes operations", () => {
        const bad = new TypedTransaction().touch(Organization("").admin(User("alice"))).touch(Organization("acme").admin(User("bob")));
        expect(() => bad.build()).toThrow(InvalidArgumentError);
        expect(bad.toString()).toMatch(/^<invalid transaction: /);
        const ok = new TypedTransaction()
            .mustNotMatch(Organization("acme").adminFilter())
            .delete(Organization("acme").member(User("alice")))
            .touch(Organization("acme").admin(User("alice")));
        expect(ok.length).toBe(2);
        expect(ok.build().toString()).toBe("[MUST_NOT_MATCH(organization:acme#admin@*), DELETE(organization:acme#member@user:alice), TOUCH(organization:acme#admin@user:alice)]");
    });

    it("typed filters and name constants", () => {
        expect(filterToString(OrganizationFilter())).toBe("organization:*#*@*");
        expect(filterToString(Organization("acme").filter())).toBe("organization:acme#*@*");
        expect(filterToString(Organization("acme").adminFilter())).toBe("organization:acme#admin@*");
        expect(filterToString(withSubject(Organization("acme").adminFilter(), User("alice")))).toBe("organization:acme#admin@user:alice#...");
        expect(filterToString(withSubject(InvitationFilter(), OrganizationCanAdminType))).toBe("invitation:*#*@organization:*#can_admin");
        expect(filterToString(withSubject(DocumentFilterOf("readme"), User))).toBe("document:readme#*@user:*");
        expect(OrganizationRelations.admin).toBe("admin");
        expect(OrganizationPermissions.canAdmin).toBe("can_admin");
        expect(() => Organization("").filter()).toThrow(InvalidArgumentError);
        const key = Invitation("inv").inviterKey(User("alice"));
        expect(filterToString(key.filter())).toBe("invitation:inv#inviter@user:alice#...");
        expect(filterMatches(key.filter(), Invitation("inv").inviter(User("alice").withExpiration(new Date(Date.now() + 1000))).relationship())).toBe(true);
    });

    it("wildcard refs and identity-only keys", () => {
        expect(Organization("acme").creator(UserWildcard()).toString()).toBe("organization:acme#creator@user:*");
        expect(Organization("acme").guest(User("bob")).toString()).toBe("organization:acme#guest@user:bob");
        const key = Invitation("inv").inviterKey(User("alice"));
        expect(key.relationship()).toEqual(relationship("invitation:inv", "inviter", "user:alice"));
        expect(Invitation("inv").inviter(User("alice").withExpiration(new Date("2100-01-01T00:00:00Z"))).key().toString()).toBe(key.toString());
    });

    it("validated by-name lookups and name lists", () => {
        const org = Organization("acme");
        expect(org.permissionByName("can_admin")).toEqual(org.canAdmin);
        expect(() => org.permissionByName("admin")).toThrow(NotInSchemaError);
        expect(org.relationByName("member")).toEqual(org.memberRelation);
        expect(org.relationshipByName("creator", UserWildcard()).toString()).toBe("organization:acme#creator@user:*");
        expect(() => org.relationshipByName("creator", User("alice"))).toThrow(SubjectNotAllowedError);
        expect(() => org.relationshipByName("nope", User("alice"))).toThrow(NotInSchemaError);
        expect(() => org.relationshipByName("toString", User("alice"))).toThrow(NotInSchemaError);
        expect(OrganizationPermissionNames).toEqual(["can_admin", "view"]);
        expect(OrganizationRelationNames).toEqual(["admin", "member", "creator", "guest"]);
    });

    it("permissions produce native check requests", () => {
        const request = Document("readme").view.checkRequest(User("alice").withIpRange({ allowedCidr: "10.0.0.0/8" }), full(), { other: 1 });
        expect(request).toMatchObject({ resource: { objectType: "document", objectId: "readme" }, permission: "view", subject: { object: { objectType: "user", objectId: "alice" } }, context: { other: 1, allowed_cidr: "10.0.0.0/8" } });
    });
});

function DocumentFilterOf(id: string) {
    return Document(id).filter();
}

describe("adoption against a live SpiceDB", () => {
    let tc: TypedClient;
    const acme = Organization("acme-adoption");
    const inv = Invitation("inv-adoption");

    beforeAll(async () => {
        tc = TypedClient.create(process.env.SPICEDB_ENDPOINT ?? "localhost:50051", process.env.SPICEDB_TOKEN ?? "somerandomkeyhere", { transportSecurity: TransportSecurity.Plaintext });
        await tc.writeSchema(readFileSync(new URL("../sample.zed", import.meta.url), "utf8"));
        for (const filter of [acme.filter(), inv.filter()])
            await tc.deleteAllRelationshipsNonAtomic(filter);
    });
    afterAll(() => tc.close());

    it("writes, checks, reads back and deletes through typed APIs", async () => {
        const later = new Date(Date.now() + 3_600_000);
        // permission-userset and wildcard subjects write and check.
        await tc.writeTransaction(new TypedTransaction()
            .touch(acme.admin(User("alice")))
            .touch(acme.member(User("bob")))
            .touch(acme.creator(UserWildcard()))
            .touch(acme.guest(UserWildcard()))
            .touch(inv.grant(acme.canAdminSubject()))
            .touch(inv.timedGrant(acme.canAdminSubject().withExpiration(later)))
            .touch(inv.inviter(User("alice").withExpiration(later))));

        expect((await tc.check(full(), inv.accept, User("alice"))).hasPermission()).toBe(true);
        expect((await tc.check(full(), acme.view, User("anyone"))).hasPermission()).toBe(true);
        expect((await tc.check(full(), acme.creatorRelation, User("anyone"))).hasPermission()).toBe(true);

        // relation checks.
        expect((await tc.check(full(), acme.memberRelation, User("bob"))).hasPermission()).toBe(true);
        expect((await tc.check(full(), acme.memberRelation, User("alice"))).hasPermission()).toBe(false);

        // typed bulk check, in input order, forwarding native options.
        const results = await tc.checkBulkTyped(full(), checksFor(User("bob"), acme.canAdmin, acme.view, acme.memberRelation), { itemErrors: BulkItemErrors.Returned });
        expect(results.map(r => r.hasPermission())).toEqual([false, true, true]);

        // a precondition guards a mixed delete+touch; a failing guard writes nothing.
        await tc.writeTransaction(new TypedTransaction()
            .mustNotMatch(withSubject(acme.adminFilter(), User("bob")))
            .delete(acme.member(User("bob")))
            .touch(acme.admin(User("bob"))));
        await expect(tc.writeTransaction(new TypedTransaction()
            .mustNotMatch(withSubject(acme.adminFilter(), User("bob")))
            .touch(acme.member(User("carol"))))).rejects.toBeInstanceOf(FailedPreconditionError);

        // read back with a typed filter and delete what was read.
        const admins: string[] = [];
        const txn = new TypedTransaction();
        for await (const r of tc.readRelationships(full(), acme.adminFilter())) {
            admins.push(r.subject.object.objectId);
            txn.delete(fromRelationship(r));
        }
        expect(admins.sort()).toEqual(["alice", "bob"]);
        await tc.writeTransaction(txn);

        // delete an expiration-only relationship by identity alone.
        await tc.delete([inv.inviterKey(User("alice"))]);
        for await (const r of tc.readRelationships(full(), inv.inviterFilter()))
            throw new Error(`inviter relationship should be deleted: ${r.relation}`);
    });
});
