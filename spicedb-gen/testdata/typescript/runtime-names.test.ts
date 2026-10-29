// The typed client over the in-memory test double, and the schema-level
// by-name lookups code that receives type and permission names at runtime
// (policy expressions, admin tools) uses. Needs no server.
import { readFileSync } from "node:fs";
import { describe, it, expect } from "vitest";
import { InMemorySpiceDB, InvalidArgumentError, LookupPermissionshipFilter, full, relationship, type SpiceDBClientInterface } from "@spicedb/client";
import {
    TypedClient, Document, User, Team, Organization, NotInSchemaError, SubjectNotAllowedError, PermissionRef, SchemaText, Definitions, DefinitionNames,
    definitionByName, permissionRefByName, subjectSetByName, checksFor,
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
        expect(db.callsOf("check")).toHaveLength(2);
    });

    it("looks up by relation, and returns nothing without a call when the subject type cannot reach", async () => {
        const db = new InMemorySpiceDB([relationship("document:d1", "viewer", "user:alice")]);
        const tc = new TypedClient(db);
        expect((await drain(tc.lookupResourcesByName(full(), "document", "viewer", User("alice")))).map(r => r.resourceId)).toEqual(["d1"]);
        expect(await drain(tc.lookupResourcesByName(full(), "document", "view", Organization("o1")))).toEqual([]);
        expect(await drain(tc.lookupResourcesByName(full(), "document", "view", Team("eng")))).toEqual([]);
        expect(db.callsOf("lookupResources")).toHaveLength(1);

        expect((await drain(tc.lookupSubjectsByName(full(), "document", "d1", "viewer", "user"))).map(s => s.subject.subjectId)).toEqual(["alice"]);
        for (const type of ["organization", "team", "organization#member"])
            expect(await drain(tc.lookupSubjectsByName(full(), "document", "d1", "view", type))).toEqual([]);
        expect(db.callsOf("lookupSubjects")).toHaveLength(1);
        expect(() => tc.lookupSubjectsByName(full(), "folder", "d1", "view", "user")).toThrow(NotInSchemaError);
        expect(() => tc.lookupSubjectsByName(full(), "document", "d1", "nope", "user")).toThrow(NotInSchemaError);
        expect(() => tc.lookupSubjectsByName(full(), "document", "d1", "view", "folder")).toThrow(NotInSchemaError);
        expect(() => tc.lookupSubjectsByName(full(), "document", "d1", "view", "team#owner")).toThrow(NotInSchemaError);
        expect(() => tc.lookupSubjectsByName(full(), "document", "d1", "view", "team#member", { subjectRelation: "other" })).toThrow(InvalidArgumentError);
        expect(db.callsOf("lookupSubjects")).toHaveLength(1);
    });
});
