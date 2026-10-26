import { SubjectRelationKind } from "../modes.js";
import { describe, it, expect } from "vitest";
import { Precondition_Operation } from "@spicedb/proto";
import { relationship, relationshipFromTuple, relationshipToFilter, Transaction, toProtoRelationship, fromProtoRelationship, toProtoRelationshipFilter, toProtoDeletePreconditions, toProtoDeleteRelationshipsRequest, jsonSnapshot, ZedToken, type Relationship, type JSONObject } from "../types.js";
import { InvalidArgumentError } from "../errors.js";
describe("native relationship values", () => {
    it("parses and round trips direct subjects", () => {
        const rel = relationship("document:readme", "viewer", "user:jimmy");
        expect(rel).toEqual({ resource: { objectType: "document", objectId: "readme" }, relation: "viewer", subject: { object: { objectType: "user", objectId: "jimmy" } } });
        expect(fromProtoRelationship(toProtoRelationship(rel))).toEqual(rel);
        expect(relationshipFromTuple("document:readme#viewer", "user:jimmy")).toEqual(rel);
    });
    it("parses and round trips userset subjects", () => {
        const rel = relationship("document:readme", "viewer", "group:eng#member");
        expect(rel.subject).toEqual({ object: { objectType: "group", objectId: "eng" }, relation: "member" });
        expect(fromProtoRelationship(toProtoRelationship(rel))).toEqual(rel);
    });
    it.each([() => relationship("invalid", "viewer", "user:a"), () => relationship("document:a", "viewer", "invalid"), () => relationshipFromTuple("document:a", "user:a")])("rejects malformed tuples", fn => expect(fn).toThrow(InvalidArgumentError));
    it("round trips caveat JSON including nested nulls and expiration", () => {
        const rel: Relationship = { ...relationship("document:a", "viewer", "user:a"), caveat: { name: "region", context: { nested: { list: [null, 1, true, "yes"] } } }, expiration: new Date("2026-01-01Z") };
        const result = fromProtoRelationship(toProtoRelationship(rel));
        expect(result).toEqual(rel);
        expect(() => result.expiration!.setTime(0)).toThrow(TypeError);
    });
    it("rejects invalid expiration before transport", () => expect(() => toProtoRelationship({ ...relationship("d:a", "r", "u:a"), expiration: new Date(NaN) })).toThrow(InvalidArgumentError));
});
describe("mutable transaction snapshot boundaries", () => {
    it("accumulates create/touch/delete and preconditions with metadata", () => {
        const rel = relationship("document:a", "viewer", "user:a");
        const txn = new Transaction().create(rel).touch(rel).delete(rel).mustMatch({ resourceType: "document" }).mustNotMatch({ resourceType: "user" }).withMetadata({ source: "test" });
        expect(txn._build().updates).toHaveLength(3);
        expect(txn._build().optionalPreconditions).toHaveLength(2);
        expect(txn._build().optionalTransactionMetadata).toEqual({ source: "test" });
    });
    it("snapshots nested caller-owned JSON, references, metadata and dates", () => {
        const context = { nested: { list: [null, 1] } };
        const meta = { nested: { value: "original" } };
        const rel = { resource: { objectType: "document", objectId: "a" }, relation: "viewer", subject: { object: { objectType: "user", objectId: "a" } }, caveat: { name: "test", context }, expiration: new Date("2026-01-01Z") };
        const txn = new Transaction().touch(rel).withMetadata(meta);
        rel.resource.objectId = "mutated";
        context.nested.list.push(2);
        meta.nested.value = "mutated";
        rel.expiration.setTime(0);
        const result = txn._build();
        expect(result.updates[0].relationship?.resource?.objectId).toBe("a");
        expect(result.updates[0].relationship?.optionalCaveat?.context).toEqual({ nested: { list: [null, 1] } });
        expect(result.optionalTransactionMetadata).toEqual({ nested: { value: "original" } });
        expect(result.updates[0].relationship?.optionalExpiresAt?.seconds).toBe(1767225600n);
    });
});
describe("exact relationship filters", () => {
    it.each([undefined, { kind: SubjectRelationKind.Any } as const, { kind: SubjectRelationKind.Direct } as const, { kind: SubjectRelationKind.Named, relation: "member" } as const])("preserves subject relation presence %j", relation => {
        const proto = toProtoRelationshipFilter({ resourceType: "document", subject: { subjectType: "user", subjectId: "a", relation } });
        expect(proto.optionalSubjectFilter?.subjectType).toBe("user");
        expect(proto.optionalSubjectFilter?.optionalSubjectId).toBe("a");
        if (relation?.kind === SubjectRelationKind.Direct)
            expect(proto.optionalSubjectFilter?.optionalRelation?.relation).toBe("");
        else if (relation?.kind === SubjectRelationKind.Named)
            expect(proto.optionalSubjectFilter?.optionalRelation?.relation).toBe("member");
        else
            expect(proto.optionalSubjectFilter?.optionalRelation).toBeUndefined();
    });
    it("converts all direct tuple identity into an exact filter", () => {
        const rel = relationship("document:a", "viewer", "user:a");
        const filter = relationshipToFilter(rel);
        expect(filter).toEqual({ resourceType: "document", resourceId: "a", relation: "viewer", subject: { subjectType: "user", subjectId: "a", relation: { kind: SubjectRelationKind.Direct } } });
        expect(toProtoRelationshipFilter(filter).optionalSubjectFilter?.optionalRelation).toBeDefined();
        expect(relationshipToFilter(relationship("document:a", "viewer", "group:a#member")).subject?.relation).toEqual({ kind: SubjectRelationKind.Named, relation: "member" });
    });
    it.each([{ resourceType: "d", resourceId: "a", resourceIdPrefix: "a" }, { resourceType: "d", subject: { subjectId: "a" } }, { resourceType: "d", subject: { relation: { kind: SubjectRelationKind.Named, relation: "member" } } }, { resourceType: "d", subject: { subjectType: "u", relation: { kind: SubjectRelationKind.Named, relation: "" } } }])("rejects unrepresentable filter %j", filter => expect(() => toProtoRelationshipFilter(filter as never)).toThrow(InvalidArgumentError));
    it("orders mustMatch before mustNotMatch and preserves direct subject filters", () => {
        expect(toProtoDeletePreconditions()).toEqual([]);
        const filter = relationshipToFilter(relationship("d:a", "r", "u:a"));
        const preconditions = toProtoDeletePreconditions({ mustMatch: [filter], mustNotMatch: [{ resourceType: "u" }] });
        expect(preconditions.map(p => p.operation)).toEqual([Precondition_Operation.MUST_MATCH, Precondition_Operation.MUST_NOT_MATCH]);
        expect(preconditions[0].filter?.optionalSubjectFilter?.optionalRelation?.relation).toBe("");
    });
    it("requires a positive bounded delete limit and permits partial deletion", () => {
        const req = toProtoDeleteRelationshipsRequest({ resourceType: "d" }, { limit: 500, cursor: "opaque", mustMatch: [{ resourceType: "d" }] });
        expect(req.optionalLimit).toBe(500);
        expect(req.optionalAllowPartialDeletions).toBe(true);
        expect(req.optionalCursor?.token).toBe("opaque");
        expect(req.optionalPreconditions).toHaveLength(1);
        for (const limit of [0, -1, 1.2, NaN, Infinity])
            expect(() => toProtoDeleteRelationshipsRequest({ resourceType: "d" }, { limit })).toThrow(InvalidArgumentError);
    });
});
describe("JSON ownership and validation", () => {
    it("deeply snapshots nulls and readonly collections", () => { const value = { a: { list: [null, 1] } }; const snapshot = jsonSnapshot(value); value.a.list.push(2); expect(snapshot).toEqual({ a: { list: [null, 1] } }); expect(Object.isFrozen(snapshot)).toBe(true); expect(Object.isFrozen((snapshot.a as JSONObject).list)).toBe(true); });
    it.each([undefined, () => { }, Symbol(), 1n, NaN, Infinity, new Date(), new Map(), new Set()])("rejects unsupported JSON with its path %s", value => expect(() => jsonSnapshot({ nested: { bad: value } })).toThrow(/context.nested.bad/));
    it("rejects cycles and sparse arrays with useful paths", () => { const cyclic: Record<string, unknown> = {}; cyclic.self = cyclic; expect(() => jsonSnapshot(cyclic)).toThrow(/context.self/); expect(() => jsonSnapshot({ list: Array(1) })).toThrow(/context.list\[0\]/); });
    it("preserves opaque tokens losslessly", () => { expect(new ZedToken("opaque+/==").toString()).toBe("opaque+/=="); expect(new ZedToken("").toString()).toBe(""); });
});
