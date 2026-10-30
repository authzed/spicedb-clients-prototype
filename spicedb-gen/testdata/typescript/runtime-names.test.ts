// The typed client over the in-memory test double, and the schema-level
// by-name lookups code that receives type and permission names at runtime
// (policy expressions, admin tools) uses. Needs no server.
import { readFileSync } from "node:fs";
import { describe, it, expect } from "vitest";
import { InMemorySpiceDB, InvalidArgumentError, LookupPermissionshipFilter, full, relationship, type SpiceDBClientInterface } from "@spicedb/client";
import {
    TypedClient, Document, User, Team, Organization, Invitation, UserWildcard, NotInSchemaError, SubjectNotAllowedError, PermissionRef, SchemaText,
    Definitions, DefinitionNames, definitionByName, permissionRefByName, subjectSetByName, checksFor, objectByName, parseObject, parseSubject, relationshipFromTriple,
    TypedTransaction, withSubject, DocumentFilter, TeamMemberType,
    type DefinitionName, type AnyResourceRef, type AnySubject,
} from "./permissions";

describe("TypedClient over InMemorySpiceDB", () => {
    it("accepts the client interface, so it wraps the double", async () => {
        const db = new InMemorySpiceDB([relationship("document:d1", "view", "user:alice"), relationship("document:d2", "view", "user:bob")]);
        const asInterface: SpiceDBClientInterface = db;
        const tc = new TypedClient(asInterface);
        expect(tc.client).toBe(db);

        // Checks resolve through the double: stored relationships, then a resolver.
        expect((await tc.check(full(), Document("d1").view, User("alice"))).hasPermission()).toBe(true);
        expect((await tc.check(full(), Document("d2").view, User("alice"))).hasPermission()).toBe(false);
        db.setCheckResolver(check => check.resource.objectId.startsWith("gen-") ? "hasPermission" : undefined);
        expect((await tc.check(full(), Document("gen-1f2e").view, User("alice"))).hasPermission()).toBe(true);

        // Typed writes and lookups go through it, and it records them.
        await tc.touch([Document("d3").viewer(User("alice"))]);
        expect(db.callsOf("write")).toHaveLength(1);
        const ids: string[] = [];
        for await (const r of tc.lookupResources(full(), Document.view, User("alice"), { permissionshipFilter: LookupPermissionshipFilter.HasPermissionOnly }))
            ids.push(r.resourceId);
        expect(ids).toEqual(["d1"]);
        expect(db.callsOf("lookupResources")[0]!.request).toMatchObject({ resourceType: "document", permission: "view" });
        db.setCheckResolver(InMemorySpiceDB.grantAll);
        const subjects: string[] = [];
        for await (const s of tc.lookupSubjects(full(), Document("d1").view, User)) subjects.push(s.subject.subjectId);
        expect(subjects).toEqual(["alice", "bob"]);
    });
});

describe("schema-level names", () => {
    it("exposes definition-name constants and the schema text", () => {
        expect(Definitions.document).toBe("document");
        expect(DefinitionNames).toEqual(["user", "team", "document", "organization", "invitation"]);
        const name: DefinitionName = Definitions.organization;
        expect(name).toBe("organization");
        expect(SchemaText).toBe(readFileSync(new URL("../sample.zed", import.meta.url), "utf8"));
    });

    it("looks up a definition by name, validated against the schema", () => {
        const doc = definitionByName("document");
        expect(doc.name).toBe("document");
        expect(doc.permissionNames).toEqual(["view", "edit", "delete"]);
        expect(doc.relationNames).toEqual(["viewer", "editor", "owner"]);
        const ref = doc.ref("d1");
        expect(String(ref)).toBe("document:d1");
        expect(String(ref.permissionByName("view"))).toBe("document:d1#view");
        expect(() => ref.permissionByName("nope")).toThrow(NotInSchemaError);
        expect(ref.relationshipByName("viewer", User("alice")).toString()).toBe("document:d1#viewer@user:alice");
        expect(() => definitionByName("folder")).toThrow(NotInSchemaError);
        expect(() => definitionByName("toString")).toThrow(NotInSchemaError);
    });

    it("builds a resource-less permission ref by names", () => {
        const p = permissionRefByName("organization", "can_admin");
        expect(p).toBeInstanceOf(PermissionRef);
        expect(String(p)).toBe("organization#can_admin");
        expect(() => permissionRefByName("organization", "admin")).toThrow(NotInSchemaError); // a relation, not a permission
        expect(() => permissionRefByName("folder", "view")).toThrow(NotInSchemaError);
    });

    it("looks up resources by runtime names, validating them before any call", async () => {
        const db = new InMemorySpiceDB([relationship("document:d1", "view", "user:alice"), relationship("organization:o1", "view", "user:alice")]);
        const tc = new TypedClient(db);
        const run = async (definition: string, permission: string) => {
            const ids: string[] = [];
            for await (const r of tc.lookupResourcesByName(full(), definition, permission, User("alice"))) ids.push(r.resourceId);
            return ids;
        };
        expect(await run("document", "view")).toEqual(["d1"]);
        expect(await run("organization", "view")).toEqual(["o1"]);
        expect(() => tc.lookupResourcesByName(full(), "document", "viewr", User("alice"))).toThrow(NotInSchemaError);
        expect(() => tc.lookupResourcesByName(full(), "folder", "view", User("alice"))).toThrow(NotInSchemaError);
        expect(() => tc.lookupResourcesByName(full(), "document", "view", { ...Team("eng").member(), _type: "group" } as never)).toThrow(NotInSchemaError);
        expect(db.callsOf("lookupResources")).toHaveLength(2);
    });
});

describe("runtime names: subject sets, any object, checks and lookups", () => {
    const drain = async <T>(it: AsyncIterableIterator<T>): Promise<T[]> => {
        const out: T[] = [];
        for await (const v of it) out.push(v);
        return out;
    };

    it("builds subject sets by name, validated against the schema", () => {
        const eng = subjectSetByName("team", "eng", "member");
        expect(String(eng)).toBe("team:eng#member");
        expect(eng._ref).toBe("TeamMemberRef");
        expect(String(subjectSetByName("organization", "acme", "can_admin"))).toBe(String(Organization("acme").canAdminSubject()));
        expect(Document("d1").relationshipByName("viewer", eng).toString()).toBe("document:d1#viewer@team:eng#member");
        expect(() => subjectSetByName("folder", "x", "member")).toThrow(NotInSchemaError);
        expect(() => subjectSetByName("team", "x", "owner")).toThrow(NotInSchemaError);
        expect(() => subjectSetByName("document", "x", "viewer")).toThrow(/not a subject anywhere/);
        expect(() => subjectSetByName("team", "x", "toString")).toThrow(NotInSchemaError);
    });

    it("builds objects from names and type:id text", () => {
        expect(String(objectByName("document", "readme"))).toBe("document:readme");
        expect(objectByName("document", "readme")._ref).toBe("DocumentRef");
        expect(parseObject("organization:acme")._ref).toBe("OrganizationRef");
        expect(String(parseObject("organization:acme"))).toBe("organization:acme");
        expect(() => objectByName("folder", "x")).toThrow(NotInSchemaError);
        expect(() => parseObject("folder:x")).toThrow(NotInSchemaError);
        expect(() => objectByName("document", "")).toThrow(InvalidArgumentError);
        for (const bad of ["document", "document:", ":x"]) expect(() => parseObject(bad)).toThrow(InvalidArgumentError);
        expect(() => subjectSetByName("team", "", "member")).toThrow(InvalidArgumentError);
    });

    it("parses subjects from type:id[#relation] text", () => {
        for (const [text, want] of [
            ["user:alice", User("alice")],
            ["user:alice#...", User("alice")],
            ["team:eng#member", Team("eng").member()],
            ["organization:acme#can_admin", Organization("acme").canAdminSubject()],
            ["user:*", UserWildcard()],
        ] as const) {
            const got = parseSubject(text);
            expect(got._ref, text).toBe(want._ref);
            expect(String(got), text).toBe(String(want));
        }
        // The result is admitted wherever the typed ref is.
        expect(String(Document("readme").relationshipByName("viewer", parseSubject("team:eng#member")))).toBe(
            String(Document("readme").viewer(Team("eng").member())),
        );
        for (const unknown of ["folder:x", "folder:x#member", "team:eng#owner", "document:x#viewer", "team:*"])
            expect(() => parseSubject(unknown), unknown).toThrow(NotInSchemaError);
        for (const bad of ["user", "user:", ":x", "team:eng#", "user:*#member", "#member"])
            expect(() => parseSubject(bad), bad).toThrow(InvalidArgumentError);
    });

    it("builds schema-checked relationships from tuple text", () => {
        const same = (a: { toString(): string }, b: { toString(): string }) => expect(a.toString()).toBe(b.toString());
        same(relationshipFromTriple("document:readme", "viewer", "user:alice"), Document("readme").viewer(User("alice")));
        same(relationshipFromTriple("document:readme", "viewer", "team:eng#member"), Document("readme").viewer(Team("eng").member()));
        same(relationshipFromTriple("organization:acme", "creator", "user:*"), Organization("acme").creator(UserWildcard()));
        same(relationshipFromTriple("document:readme", "editor", "user:bob#..."), Document("readme").editor(User("bob")));
        const at = new Date("2030-01-02T03:04:05Z");
        same(relationshipFromTriple("invitation:i1", "inviter", "user:alice", { expiration: at }), Invitation("i1").inviter(User("alice").withExpiration(at)));
        same(
            relationshipFromTriple("document:readme", "viewer", "user:alice", { caveat: { name: "ip_range", context: { allowed_cidr: "10.0.0.0/8" } } }),
            Document("readme").viewer(User("alice").withIpRange({ allowedCidr: "10.0.0.0/8" })),
        );

        expect(() => relationshipFromTriple("folder:x", "viewer", "user:alice")).toThrow(NotInSchemaError);
        expect(() => relationshipFromTriple("document:x", "viewer", "robot:r")).toThrow(NotInSchemaError);
        expect(() => relationshipFromTriple("document:x", "view", "user:alice")).toThrow(NotInSchemaError); // a permission
        expect(() => relationshipFromTriple("document:x", "editor", "team:eng#member")).toThrow(SubjectNotAllowedError);
        expect(() => relationshipFromTriple("document:x", "viewer", "user:*")).toThrow(SubjectNotAllowedError);
        expect(() => relationshipFromTriple("organization:x", "creator", "user:alice")).toThrow(SubjectNotAllowedError);
        expect(() => relationshipFromTriple("invitation:x", "inviter", "user:alice")).toThrow(SubjectNotAllowedError);
        expect(() => relationshipFromTriple("document:x", "editor", "user:alice", { expiration: at })).toThrow(SubjectNotAllowedError);
        expect(() => relationshipFromTriple("document:x", "editor", "user:alice", { caveat: { name: "ip_range" } })).toThrow(SubjectNotAllowedError);
        expect(() => relationshipFromTriple("document", "viewer", "user:alice")).toThrow(InvalidArgumentError);
        expect(() => relationshipFromTriple("document:x", "viewer", "alice")).toThrow(InvalidArgumentError);
        expect(() => relationshipFromTriple("document:x", "viewer", "team:eng#")).toThrow(InvalidArgumentError);
    });

    it("covers every definition ref with AnyResourceRef", () => {
        const refs: AnyResourceRef[] = DefinitionNames.map(name => definitionByName(name).ref("x"));
        expect(refs.map(r => r._type)).toEqual([...DefinitionNames]);
        const asSubject: AnySubject = refs[0]!;
        expect(String(asSubject)).toBe("user:x");
    });

    it("checks by name against the double", async () => {
        const db = new InMemorySpiceDB([relationship("document:d1", "view", "user:alice")]);
        const tc = new TypedClient(db);
        expect((await tc.checkByName(full(), "document", "d1", "view", User("alice"))).hasPermission()).toBe(true);
        expect((await tc.checkByName(full(), "document", "d1", "view", User("bob"))).hasPermission()).toBe(false);
        // A by-name permission also checks in bulk.
        const perm = definitionByName("document").ref("d1").permissionByName("view");
        const [result] = await tc.checkBulkTyped(full(), checksFor(User("alice"), perm));
        expect(result!.hasPermission()).toBe(true);
        await expect(tc.checkByName(full(), "document", "d1", "nope", User("alice"))).rejects.toThrow(NotInSchemaError);
        await expect(tc.checkByName(full(), "document", "d1", "view", Organization("o1"))).rejects.toThrow(SubjectNotAllowedError);
        await expect(tc.checkByName(full(), "organization", "o1", "view", UserWildcard())).rejects.toThrow(SubjectNotAllowedError);
        expect(db.callsOf("check")).toHaveLength(2);

        const results = await tc.checkBulkByName(full(), [
            { definition: "document", id: "d1", permission: "view", subject: User("alice") },
            { definition: "document", id: "d1", permission: "view", subject: User("bob") },
        ]);
        expect(results.map(r => r.hasPermission())).toEqual([true, false]);
        await expect(tc.checkBulkByName(full(), [
            { definition: "document", id: "d1", permission: "view", subject: User("alice") },
            { definition: "document", id: "d1", permission: "share", subject: User("alice") },
        ])).rejects.toThrow(NotInSchemaError);
        expect(db.callsOf("checkBulk")).toHaveLength(2);
    });

    it("looks up by relation, and returns nothing without a call when the subject type cannot reach", async () => {
        const db = new InMemorySpiceDB([relationship("document:d1", "viewer", "user:alice")]);
        const tc = new TypedClient(db);
        expect((await drain(tc.lookupResourcesByName(full(), "document", "viewer", User("alice")))).map(r => r.resourceId)).toEqual(["d1"]);
        expect(await drain(tc.lookupResourcesByName(full(), "document", "view", Organization("o1")))).toEqual([]);
        expect(await drain(tc.lookupResourcesByName(full(), "document", "view", Team("eng")))).toEqual([]);
        expect(() => tc.lookupResourcesByName(full(), "organization", "view", UserWildcard())).toThrow(SubjectNotAllowedError);
        expect(db.callsOf("lookupResources")).toHaveLength(1);

        expect((await drain(tc.lookupSubjectsByName(full(), "document", "d1", "viewer", "user"))).map(s => s.subject.subjectId)).toEqual(["alice"]);
        for (const [type, relation] of [["organization", undefined], ["team", undefined], ["organization", "member"]] as const)
            expect(await drain(tc.lookupSubjectsByName(full(), "document", "d1", "view", type, relation))).toEqual([]);
        expect(db.callsOf("lookupSubjects")).toHaveLength(1);
        expect(() => tc.lookupSubjectsByName(full(), "folder", "d1", "view", "user")).toThrow(NotInSchemaError);
        expect(() => tc.lookupSubjectsByName(full(), "document", "d1", "nope", "user")).toThrow(NotInSchemaError);
        expect(() => tc.lookupSubjectsByName(full(), "document", "d1", "view", "folder")).toThrow(NotInSchemaError);
        expect(() => tc.lookupSubjectsByName(full(), "document", "d1", "view", "team", "owner")).toThrow(NotInSchemaError);
        expect(() => tc.lookupSubjectsByName(full(), "document", "d1", "view", "team", "member", { subjectRelation: "other" })).toThrow(InvalidArgumentError);
        expect(db.callsOf("lookupSubjects")).toHaveLength(1);
    });
});

describe("null and undefined from untyped callers", () => {
    it("are local validation errors, never a TypeError", async () => {
        const db = new InMemorySpiceDB([]);
        const tc = new TypedClient(db);
        // What a JavaScript caller, or a value cast past the types, can pass.
        const none = null as unknown as string;
        const noSubject = undefined as unknown as AnySubject;
        const drain = async (it: AsyncIterable<unknown>) => {
            for await (const _ of it);
        };
        const calls: (() => unknown)[] = [
            () => definitionByName(none),
            () => permissionRefByName("document", none),
            () => objectByName("document", none),
            () => parseObject(none),
            () => parseSubject(none),
            () => subjectSetByName("team", "eng", none),
            () => relationshipFromTriple(none, "viewer", "user:alice"),
            () => relationshipFromTriple("document:readme", none, "user:alice"),
            () => relationshipFromTriple("document:readme", "viewer", none),
            () => Document("readme").relationshipByName("viewer", noSubject),
            () => Document("readme").viewer(noSubject as never).relationship(),
            () => tc.check(full(), Document("readme").view, noSubject as never),
            () => tc.checkByName(full(), none, "readme", "view", User("alice")),
            () => tc.checkByName(full(), "document", none, "view", User("alice")),
            () => tc.checkByName(full(), "document", "readme", none, User("alice")),
            () => tc.checkByName(full(), "document", "readme", "view", noSubject),
            () => drain(tc.lookupResourcesByName(full(), "document", "view", noSubject)),
            () => drain(tc.lookupSubjectsByName(full(), "document", none, "view", "user")),
        ];
        for (const [i, call] of calls.entries()) {
            await expect((async () => call())(), `call ${i}`).rejects.toThrow(InvalidArgumentError);
        }
        expect(db.calls).toHaveLength(0);
    });
});

describe("wrong-typed values from untyped callers", () => {
    // What a JavaScript caller, or a value cast past the types, can pass:
    // undefined or null inside an array or object, and a name or ID of the
    // wrong type. Each is a local validation error before any call, never a
    // TypeError and never a coerced value sent to the server.
    const db = new InMemorySpiceDB([]);
    const tc = new TypedClient(db);
    const num = 123 as unknown as string;
    const list = ["document"] as unknown as string;
    const none = undefined as never;
    const drain = async (it: AsyncIterable<unknown>) => {
        for await (const _ of it);
    };
    const named = { definition: "document", id: "readme", permission: "view", subject: User("alice") };
    const calls: [string, () => unknown][] = [
        ["checkBulkByName with an undefined item", () => tc.checkBulkByName(full(), [none])],
        ["checkBulkByName with a null item", () => tc.checkBulkByName(full(), [null as never])],
        ["checkBulkByName with a non-array", () => tc.checkBulkByName(full(), none)],
        ["checkBulkByName with an undefined subject", () => tc.checkBulkByName(full(), [{ ...named, subject: none }])],
        ["checkBulkByName with a numeric ID", () => tc.checkBulkByName(full(), [{ ...named, id: num }])],
        ["checkBulkByName with an array name", () => tc.checkBulkByName(full(), [{ ...named, permission: list }])],
        ["checkBulkTyped with an undefined item", () => tc.checkBulkTyped(full(), [none])],
        ["checkBulkTyped with an undefined permission", () => tc.checkBulkTyped(full(), [{ permission: none, subject: User("alice") }])],
        ["checkBulkTyped with a non-array", () => tc.checkBulkTyped(full(), none)],
        ["checksFor with an undefined permission", () => checksFor(User("alice"), Document("readme").view, none)],
        ["check with an undefined permission", () => tc.check(full(), none, User("alice"))],
        ["check with a number subject", () => tc.check(full(), Document("readme").view, 5 as never)],
        ["check with a numeric resource ID", () => tc.check(full(), Document(num).view, User("alice"))],
        ["check with a numeric subject ID", () => tc.check(full(), Document("readme").view, User(num))],
        ["checkByName with a numeric ID", () => tc.checkByName(full(), "document", num, "view", User("alice"))],
        ["checkByName with an array definition", () => tc.checkByName(full(), list, "readme", "view", User("alice"))],
        ["checkByName with a string subject", () => tc.checkByName(full(), "document", "readme", "view", "user:alice" as never)],
        ["touch with an undefined item", () => tc.touch([Document("readme").viewer(User("alice")), none])],
        ["touch with a non-array", () => tc.touch(none)],
        ["delete with a null item", () => tc.delete([null as never])],
        ["writeTransaction with a null precondition", () => tc.writeTransaction(new TypedTransaction().mustMatch(null as never))],
        ["writer with a numeric resource ID", () => Document(num).viewer(User("alice")).relationship()],
        ["writer with a number subject", () => Document("readme").viewer(5 as never).relationship()],
        ["withExpiration with no date", () => Invitation("i").inviter(User("alice").withExpiration(none))],
        ["withIpRange with no context", () => User("alice").withIpRange(none)],
        ["lookupResources with an undefined permission", () => drain(tc.lookupResources(full(), none, User("alice")))],
        ["lookupResources with a numeric subject ID", () => drain(tc.lookupResources(full(), Document.view, User(num)))],
        ["lookupResourcesByName with a numeric permission", () => drain(tc.lookupResourcesByName(full(), "document", num, User("alice")))],
        ["lookupSubjects with an undefined permission", () => drain(tc.lookupSubjects(full(), none, User))],
        ["lookupSubjects with a string subject type", () => drain(tc.lookupSubjects(full(), Document("readme").view, "user" as never))],
        ["lookupSubjectsByName with a numeric ID", () => drain(tc.lookupSubjectsByName(full(), "document", num, "view", "user"))],
        ["lookupSubjectsByName with a numeric subject relation", () => drain(tc.lookupSubjectsByName(full(), "document", "readme", "view", "team", num))],
        ["readRelationships with an undefined filter", () => drain(tc.readRelationships(full(), none))],
        ["withSubject with a number subject", () => withSubject(DocumentFilter(), 5 as never)],
        ["withSubject with an undefined filter", () => withSubject(none, User("alice"))],
        ["definitionByName with a number", () => definitionByName(num)],
        ["definitionByName with an array", () => definitionByName(list)],
        ["permissionRefByName with an array", () => permissionRefByName("document", list)],
        ["objectByName with a numeric ID", () => objectByName("document", num)],
        ["subjectSetByName with a numeric ID", () => subjectSetByName("team", num, "member")],
        ["parseObject with a number", () => parseObject(num)],
        ["parseSubject with an array", () => parseSubject(list)],
        ["permissionByName with a number", () => Document("readme").permissionByName(num)],
        ["relationshipByName with a number subject", () => Document("readme").relationshipByName("viewer", 5 as never)],
        ["relationshipFromTriple with a numeric relation", () => relationshipFromTriple("document:readme", num, "user:alice")],
        ["relationshipFromTriple with a null caveat", () => relationshipFromTriple("document:readme", "viewer", "user:alice", { caveat: null as never })],
        ["relationshipFromTriple with a string expiration", () => relationshipFromTriple("invitation:i", "inviter", "user:alice", { expiration: "tomorrow" as never })],
    ];
    for (const [name, call] of calls) {
        it(`${name} is an InvalidArgumentError`, async () => {
            const result = (async () => call())();
            await expect(result).rejects.toThrow(InvalidArgumentError);
            await expect(result).rejects.not.toThrow(NotInSchemaError);
            expect(db.calls).toHaveLength(0);
        });
    }

    it("still accepts well-typed values", async () => {
        // The double answers checks from stored relationships, not the schema.
        const ok = new InMemorySpiceDB([relationship("document:readme", "view", "user:alice")]);
        const typed = new TypedClient(ok);
        const results = await typed.checkBulkByName(full(), [named]);
        expect(results.map((r) => r.hasPermission())).toEqual([true]);
        expect((await typed.checkBulkTyped(full(), checksFor(User("alice"), Document("readme").view)))[0]!.hasPermission()).toBe(true);
        await typed.touch([Document("readme").viewer(User("bob"))]);
        const at = new Date(Date.now() + 60_000);
        expect(Invitation("i").inviter(User("alice").withExpiration(at)).relationship().expiration).toEqual(at);
        expect(User("alice").withIpRange({ allowedCidr: "10.0.0.0/8" })._caveatContext).toEqual({ allowed_cidr: "10.0.0.0/8" });
        expect(UserWildcard().toString()).toBe("user:*");
        expect(Organization("o").canAdminSubject().withExpiration(at)._expiration).toEqual(at);
        expect(withSubject(DocumentFilter(), TeamMemberType).subject).toMatchObject({ subjectType: "team" });
        expect(relationshipFromTriple("invitation:i", "inviter", "user:alice", { expiration: at }).relationship().expiration).toEqual(at);
        expect(typed.lookupSubjectsByName(full(), "document", "readme", "view", "user", null as never)).toBeDefined();
    });
});
