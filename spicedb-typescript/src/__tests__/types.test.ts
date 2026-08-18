import { describe, it, expect } from "vitest";
import { Precondition_Operation } from "@spicedb/proto";
import {
  relationship,
  relationshipFromTuple,
  Transaction,
  toProtoRelationship,
  fromProtoRelationship,
  toProtoRelationshipFilter,
  toProtoDeletePreconditions,
  toProtoDeleteRelationshipsRequest,
} from "../types.js";
import { InvalidArgumentError } from "../errors.js";

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

describe("toProtoRelationshipFilter()", () => {
  // Regression test for the offboarding hazard this finding describes:
  // toProtoRelationshipFilter used to build optionalSubjectFilter only
  // inside `if (filter.subjectType)`, so `{ resourceType: "document",
  // subjectId: "alice" }` silently produced a proto filter with NO subject
  // constraint at all -- deleteRelationships called with that filter would
  // delete every relationship on every document, not just alice's. It must
  // now throw instead of silently widening.
  it("throws InvalidArgumentError naming subjectId when subjectType is missing", () => {
    const call = () =>
      toProtoRelationshipFilter({ resourceType: "document", subjectId: "alice" });
    expect(call).toThrow(InvalidArgumentError);
    expect(call).toThrow(/subjectId/);
    expect(call).toThrow(/subjectType/);
  });

  it("throws InvalidArgumentError naming subjectRelation when subjectType is missing", () => {
    const call = () =>
      toProtoRelationshipFilter({
        resourceType: "document",
        subjectRelation: "member",
      });
    expect(call).toThrow(InvalidArgumentError);
    expect(call).toThrow(/subjectRelation/);
    expect(call).toThrow(/subjectType/);
  });

  // Companion to the two throw cases above -- proves subjectType alone (no
  // subjectId) still builds a valid subject filter and is not accidentally
  // caught by the new guard.
  it("does not throw when subjectType is set alone", () => {
    const proto = toProtoRelationshipFilter({
      resourceType: "document",
      subjectType: "user",
    });
    expect(proto.optionalSubjectFilter?.subjectType).toBe("user");
    expect(proto.optionalSubjectFilter?.optionalSubjectId).toBe("");
  });

  // Companion proving the valid combination (subjectType supplied alongside
  // subjectId) still works correctly.
  it("does not throw when subjectType and subjectId are both set", () => {
    const proto = toProtoRelationshipFilter({
      resourceType: "document",
      subjectType: "user",
      subjectId: "alice",
    });
    expect(proto.optionalSubjectFilter?.subjectType).toBe("user");
    expect(proto.optionalSubjectFilter?.optionalSubjectId).toBe("alice");
  });
});

describe("toProtoDeletePreconditions()", () => {
  it("returns an empty array when no options are given", () => {
    expect(toProtoDeletePreconditions()).toEqual([]);
  });

  it("returns an empty array when options has no mustMatch/mustNotMatch", () => {
    expect(toProtoDeletePreconditions({ limit: 10 })).toEqual([]);
  });

  it("builds MUST_MATCH preconditions from mustMatch filters", () => {
    const preconditions = toProtoDeletePreconditions({
      mustMatch: [{ resourceType: "document", resourceId: "readme" }],
    });
    expect(preconditions).toHaveLength(1);
    expect(preconditions[0].operation).toBe(
      Precondition_Operation.MUST_MATCH,
    );
    expect(preconditions[0].filter?.resourceType).toBe("document");
    expect(preconditions[0].filter?.optionalResourceId).toBe("readme");
  });

  it("builds MUST_NOT_MATCH preconditions from mustNotMatch filters", () => {
    const preconditions = toProtoDeletePreconditions({
      mustNotMatch: [{ resourceType: "document", resourceId: "secret" }],
    });
    expect(preconditions).toHaveLength(1);
    expect(preconditions[0].operation).toBe(
      Precondition_Operation.MUST_NOT_MATCH,
    );
    expect(preconditions[0].filter?.optionalResourceId).toBe("secret");
  });

  it("orders mustMatch preconditions before mustNotMatch, supporting multiple of each", () => {
    const preconditions = toProtoDeletePreconditions({
      mustMatch: [
        { resourceType: "document", resourceId: "a" },
        { resourceType: "document", resourceId: "b" },
      ],
      mustNotMatch: [{ resourceType: "document", resourceId: "c" }],
    });
    expect(preconditions).toHaveLength(3);
    expect(
      preconditions.map((p) => [p.operation, p.filter?.optionalResourceId]),
    ).toEqual([
      [Precondition_Operation.MUST_MATCH, "a"],
      [Precondition_Operation.MUST_MATCH, "b"],
      [Precondition_Operation.MUST_NOT_MATCH, "c"],
    ]);
  });
});

describe("toProtoDeleteRelationshipsRequest()", () => {
  it("defaults to no preconditions, zero limit, and allowPartialDeletions=false", () => {
    const req = toProtoDeleteRelationshipsRequest({
      resourceType: "document",
    });
    expect(req.relationshipFilter?.resourceType).toBe("document");
    expect(req.optionalPreconditions).toEqual([]);
    expect(req.optionalLimit).toBe(0);
    expect(req.optionalAllowPartialDeletions).toBe(false);
  });

  it("threads mustMatch/mustNotMatch into optionalPreconditions", () => {
    const req = toProtoDeleteRelationshipsRequest(
      { resourceType: "document" },
      {
        mustMatch: [{ resourceType: "document", resourceId: "readme" }],
        mustNotMatch: [{ resourceType: "document", resourceId: "secret" }],
      },
    );
    expect(req.optionalPreconditions).toHaveLength(2);
    expect(req.optionalPreconditions[0].operation).toBe(
      Precondition_Operation.MUST_MATCH,
    );
    expect(req.optionalPreconditions[1].operation).toBe(
      Precondition_Operation.MUST_NOT_MATCH,
    );
    // Setting preconditions alone must not imply a limit or partial deletion.
    expect(req.optionalLimit).toBe(0);
    expect(req.optionalAllowPartialDeletions).toBe(false);
  });

  it("sets optionalLimit and optionalAllowPartialDeletions=true when limit is provided", () => {
    const req = toProtoDeleteRelationshipsRequest(
      { resourceType: "document" },
      { limit: 500 },
    );
    expect(req.optionalLimit).toBe(500);
    expect(req.optionalAllowPartialDeletions).toBe(true);
  });

  it("combines preconditions and limit in a single request", () => {
    const req = toProtoDeleteRelationshipsRequest(
      { resourceType: "document" },
      {
        mustMatch: [{ resourceType: "document", resourceId: "readme" }],
        limit: 100,
      },
    );
    expect(req.optionalPreconditions).toHaveLength(1);
    expect(req.optionalLimit).toBe(100);
    expect(req.optionalAllowPartialDeletions).toBe(true);
  });

  // Proves the fix for the offboarding hazard this finding describes: a
  // filter carrying a subject ID but no subject type must be rejected
  // before deleteRelationships can build a request at all -- not silently
  // sent as an unconstrained-subject delete that would remove every
  // relationship on every document.
  it("throws InvalidArgumentError instead of building a request when the filter's subjectId has no subjectType", () => {
    expect(() =>
      toProtoDeleteRelationshipsRequest({
        resourceType: "document",
        subjectId: "alice",
      }),
    ).toThrow(InvalidArgumentError);
  });
});
