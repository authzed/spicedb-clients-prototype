import { describe, it, expect } from "vitest";
import { create } from "@bufbuild/protobuf";
import {
  LookupResourcesResponseSchema,
  LookupSubjectsResponseSchema,
  ResolvedSubjectSchema,
  PartialCaveatInfoSchema,
  LookupPermissionship,
} from "@spicedb/proto";
import {
  permissionshipFromProto,
  partialCaveatFromProto,
  resolvedSubjectFromProto,
  fromProtoLookupResource,
  fromProtoLookupSubject,
} from "../types.js";

describe("permissionshipFromProto()", () => {
  it("maps HAS_PERMISSION", () => {
    expect(
      permissionshipFromProto(LookupPermissionship.HAS_PERMISSION),
    ).toBe("hasPermission");
  });

  it("maps CONDITIONAL_PERMISSION", () => {
    expect(
      permissionshipFromProto(LookupPermissionship.CONDITIONAL_PERMISSION),
    ).toBe("conditionalPermission");
  });

  it("maps UNSPECIFIED", () => {
    expect(permissionshipFromProto(LookupPermissionship.UNSPECIFIED)).toBe(
      "unspecified",
    );
  });
});

describe("partialCaveatFromProto()", () => {
  it("maps undefined to undefined", () => {
    expect(partialCaveatFromProto(undefined)).toBeUndefined();
  });

  it("maps a present PartialCaveatInfo", () => {
    const proto = create(PartialCaveatInfoSchema, {
      missingRequiredContext: ["region", "time"],
    });
    expect(partialCaveatFromProto(proto)).toEqual({
      missingRequiredContext: ["region", "time"],
    });
  });
});

describe("resolvedSubjectFromProto()", () => {
  it("maps subjectObjectId, permissionship, and partialCaveat", () => {
    const proto = create(ResolvedSubjectSchema, {
      subjectObjectId: "sally",
      permissionship: LookupPermissionship.CONDITIONAL_PERMISSION,
      partialCaveatInfo: create(PartialCaveatInfoSchema, {
        missingRequiredContext: ["region"],
      }),
    });
    expect(resolvedSubjectFromProto(proto)).toEqual({
      subjectId: "sally",
      permissionship: "conditionalPermission",
      partialCaveat: { missingRequiredContext: ["region"] },
    });
  });

  it("maps an undefined input to a zero-value ResolvedSubject", () => {
    expect(resolvedSubjectFromProto(undefined)).toEqual({
      subjectId: "",
      permissionship: "unspecified",
      partialCaveat: undefined,
    });
  });
});

describe("fromProtoLookupResource()", () => {
  it("maps a HAS_PERMISSION result with no partial caveat", () => {
    const resp = create(LookupResourcesResponseSchema, {
      resourceObjectId: "readme",
      permissionship: LookupPermissionship.HAS_PERMISSION,
    });
    expect(fromProtoLookupResource(resp)).toEqual({
      resourceId: "readme",
      permissionship: "hasPermission",
      partialCaveat: undefined,
    });
  });

  it("maps a CONDITIONAL_PERMISSION result with its partial caveat", () => {
    const resp = create(LookupResourcesResponseSchema, {
      resourceObjectId: "design",
      permissionship: LookupPermissionship.CONDITIONAL_PERMISSION,
      partialCaveatInfo: create(PartialCaveatInfoSchema, {
        missingRequiredContext: ["is_in_region"],
      }),
    });
    expect(fromProtoLookupResource(resp)).toEqual({
      resourceId: "design",
      permissionship: "conditionalPermission",
      partialCaveat: { missingRequiredContext: ["is_in_region"] },
    });
  });
});

describe("fromProtoLookupSubject()", () => {
  it("surfaces a wildcard subject with two excluded subjects (modern fields)", () => {
    const resp = create(LookupSubjectsResponseSchema, {
      subject: create(ResolvedSubjectSchema, {
        subjectObjectId: "*",
        permissionship: LookupPermissionship.HAS_PERMISSION,
      }),
      excludedSubjects: [
        create(ResolvedSubjectSchema, {
          subjectObjectId: "eve",
          permissionship: LookupPermissionship.HAS_PERMISSION,
        }),
        create(ResolvedSubjectSchema, {
          subjectObjectId: "mallory",
          permissionship: LookupPermissionship.HAS_PERMISSION,
        }),
      ],
    });
    const result = fromProtoLookupSubject(resp);
    expect(result.subject.subjectId).toBe("*");
    expect(result.excludedSubjects).toHaveLength(2);
    expect(result.excludedSubjects.map((s) => s.subjectId)).toEqual([
      "eve",
      "mallory",
    ]);
  });

  it("distinguishes HAS_PERMISSION vs CONDITIONAL_PERMISSION on the subject", () => {
    const resp = create(LookupSubjectsResponseSchema, {
      subject: create(ResolvedSubjectSchema, {
        subjectObjectId: "jimmy",
        permissionship: LookupPermissionship.CONDITIONAL_PERMISSION,
        partialCaveatInfo: create(PartialCaveatInfoSchema, {
          missingRequiredContext: ["region"],
        }),
      }),
    });
    const result = fromProtoLookupSubject(resp);
    expect(result.subject).toEqual({
      subjectId: "jimmy",
      permissionship: "conditionalPermission",
      partialCaveat: { missingRequiredContext: ["region"] },
    });
  });

  it("falls back to deprecated subjectObjectId when subject is unset", () => {
    const resp = create(LookupSubjectsResponseSchema, {
      subjectObjectId: "sally",
      permissionship: LookupPermissionship.HAS_PERMISSION,
    });
    const result = fromProtoLookupSubject(resp);
    expect(result.subject).toEqual({
      subjectId: "sally",
      permissionship: "hasPermission",
      partialCaveat: undefined,
    });
  });

  it("falls back to deprecated excludedSubjectIds when excludedSubjects is empty", () => {
    const resp = create(LookupSubjectsResponseSchema, {
      subjectObjectId: "*",
      permissionship: LookupPermissionship.HAS_PERMISSION,
      excludedSubjectIds: ["eve", "mallory"],
    });
    const result = fromProtoLookupSubject(resp);
    expect(result.subject.subjectId).toBe("*");
    expect(result.excludedSubjects).toEqual([
      { subjectId: "eve", permissionship: "unspecified", partialCaveat: undefined },
      { subjectId: "mallory", permissionship: "unspecified", partialCaveat: undefined },
    ]);
  });

  it("prefers modern excludedSubjects over deprecated excludedSubjectIds when both are present", () => {
    const resp = create(LookupSubjectsResponseSchema, {
      subject: create(ResolvedSubjectSchema, { subjectObjectId: "*" }),
      excludedSubjects: [
        create(ResolvedSubjectSchema, {
          subjectObjectId: "eve",
          permissionship: LookupPermissionship.HAS_PERMISSION,
        }),
      ],
      excludedSubjectIds: ["stale-legacy-id"],
    });
    const result = fromProtoLookupSubject(resp);
    expect(result.excludedSubjects).toEqual([
      { subjectId: "eve", permissionship: "hasPermission", partialCaveat: undefined },
    ]);
  });
});
