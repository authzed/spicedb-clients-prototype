import { describe, it, expect } from "vitest";
import {
  relationship,
  relationshipFromTuple,
  Transaction,
  toProtoRelationship,
  fromProtoRelationship,
} from "../types.js";

describe("relationship()", () => {
  it("parses simple resource and subject references", () => {
    const rel = relationship("document:readme", "viewer", "user:jimmy");
    expect(rel).toEqual({
      resourceType: "document",
      resourceId: "readme",
      resourceRelation: "viewer",
      subjectType: "user",
      subjectId: "jimmy",
    });
  });

  it("parses subject with relation", () => {
    const rel = relationship("document:readme", "viewer", "group:eng#member");
    expect(rel).toEqual({
      resourceType: "document",
      resourceId: "readme",
      resourceRelation: "viewer",
      subjectType: "group",
      subjectId: "eng",
      subjectRelation: "member",
    });
  });

  it("throws on invalid resource reference", () => {
    expect(() => relationship("invalid", "viewer", "user:jimmy")).toThrow(
      'Invalid resource reference: "invalid"',
    );
  });

  it("throws on invalid subject reference", () => {
    expect(() => relationship("document:readme", "viewer", "invalid")).toThrow(
      'Invalid subject reference: "invalid"',
    );
  });
});

describe("relationshipFromTuple()", () => {
  it("parses resource tuple and subject", () => {
    const rel = relationshipFromTuple("document:readme#viewer", "user:jimmy");
    expect(rel).toEqual({
      resourceType: "document",
      resourceId: "readme",
      resourceRelation: "viewer",
      subjectType: "user",
      subjectId: "jimmy",
    });
  });

  it("throws on invalid tuple (missing #)", () => {
    expect(() =>
      relationshipFromTuple("document:readme", "user:jimmy"),
    ).toThrow('Invalid resource tuple');
  });
});

describe("toProtoRelationship / fromProtoRelationship", () => {
  it("round-trips a basic relationship", () => {
    const rel = relationship("document:readme", "viewer", "user:jimmy");
    const proto = toProtoRelationship(rel);
    const back = fromProtoRelationship(proto);
    expect(back).toEqual(rel);
  });

  it("round-trips a relationship with caveat", () => {
    const rel = {
      resourceType: "document",
      resourceId: "readme",
      resourceRelation: "viewer",
      subjectType: "user",
      subjectId: "jimmy",
      caveatName: "is_in_region",
      caveatContext: { region: "us-east-1" },
    };
    const proto = toProtoRelationship(rel);
    const back = fromProtoRelationship(proto);
    expect(back.caveatName).toBe("is_in_region");
    expect(back.caveatContext).toEqual({ region: "us-east-1" });
  });

  it("round-trips a relationship with expiration", () => {
    const expiration = new Date("2025-06-01T00:00:00Z");
    const rel = {
      resourceType: "document",
      resourceId: "readme",
      resourceRelation: "viewer",
      subjectType: "user",
      subjectId: "jimmy",
      expiration,
    };
    const proto = toProtoRelationship(rel);
    const back = fromProtoRelationship(proto);
    expect(back.expiration?.getTime()).toBe(expiration.getTime());
  });

  it("round-trips a relationship with subject relation", () => {
    const rel = relationship("document:readme", "viewer", "group:eng#member");
    const proto = toProtoRelationship(rel);
    const back = fromProtoRelationship(proto);
    expect(back.subjectRelation).toBe("member");
  });
});

describe("Transaction", () => {
  it("accumulates create/touch/delete operations", () => {
    const txn = new Transaction();
    const rel = relationship("document:readme", "viewer", "user:jimmy");
    txn.create(rel).touch(rel).delete(rel);
    expect(txn.updates).toHaveLength(3);
  });

  it("supports preconditions", () => {
    const txn = new Transaction();
    txn.mustNotMatch({ resourceType: "document" });
    txn.mustMatch({ resourceType: "document", resourceId: "readme" });
    expect(txn.preconditions).toHaveLength(2);
  });

  it("supports metadata", () => {
    const txn = new Transaction();
    txn.withMetadata({ source: "test" });
    expect(txn.metadata).toEqual({ source: "test" });
  });

  it("is chainable", () => {
    const rel = relationship("document:readme", "viewer", "user:jimmy");
    const txn = new Transaction()
      .create(rel)
      .touch(rel)
      .delete(rel)
      .mustNotMatch({ resourceType: "document" })
      .withMetadata({ source: "test" });
    expect(txn.updates).toHaveLength(3);
    expect(txn.preconditions).toHaveLength(1);
    expect(txn.metadata).toEqual({ source: "test" });
  });
});
