import { describe, it, expect, vi } from "vitest";
import { create } from "@bufbuild/protobuf";
import {
  CheckPermissionResponseSchema,
  CheckBulkPermissionsResponseItemSchema,
  PartialCaveatInfoSchema,
  ZedTokenSchema,
  CheckPermissionResponse_Permissionship,
} from "@spicedb/proto";
import {
  CheckResult,
  checkPermissionshipFromProto,
  checkResultFromProto,
  checkResultFromBulkItem,
} from "../types.js";
import { SpiceDBClient } from "../client.js";
import { full } from "../consistency.js";
import { PermissionDeniedError, NotFoundError } from "../errors.js";

// ---------------------------------------------------------------------------
// Pure mapper tests
// ---------------------------------------------------------------------------

describe("checkPermissionshipFromProto()", () => {
  it("maps HAS_PERMISSION", () => {
    expect(
      checkPermissionshipFromProto(
        CheckPermissionResponse_Permissionship.HAS_PERMISSION,
      ),
    ).toBe("hasPermission");
  });

  it("maps NO_PERMISSION (a value CheckPermissionResponse has that LookupPermissionship does not)", () => {
    expect(
      checkPermissionshipFromProto(
        CheckPermissionResponse_Permissionship.NO_PERMISSION,
      ),
    ).toBe("noPermission");
  });

  it("maps CONDITIONAL_PERMISSION", () => {
    expect(
      checkPermissionshipFromProto(
        CheckPermissionResponse_Permissionship.CONDITIONAL_PERMISSION,
      ),
    ).toBe("conditionalPermission");
  });

  it("maps UNSPECIFIED", () => {
    expect(
      checkPermissionshipFromProto(
        CheckPermissionResponse_Permissionship.UNSPECIFIED,
      ),
    ).toBe("unspecified");
  });
});

// ---------------------------------------------------------------------------
// T1 — THE regression guard on the fail-open this task exists to close.
//
// Before this change, spicedb-typescript's checkPermission/checkPermissions
// collapsed CONDITIONAL_PERMISSION into `true` (client.ts OR'd
// HAS_PERMISSION and CONDITIONAL_PERMISSION together), granting access on a
// caveat the server could not evaluate because the required context was
// never supplied. This is THE single highest-value test in the whole
// cross-client CheckResult effort: hasPermission() must be true for
// HAS_PERMISSION and ONLY for HAS_PERMISSION. If this test ever passes for
// the wrong reason, the fail-open is back.
// ---------------------------------------------------------------------------
describe("CheckResult.hasPermission() — fail-open regression guard (T1)", () => {
  it.each([
    ["unspecified", false],
    ["noPermission", false],
    ["hasPermission", true],
    ["conditionalPermission", false],
  ] as const)(
    "permissionship=%s -> hasPermission()=%s",
    (permissionship, expected) => {
      const result = new CheckResult(permissionship, [], "");
      expect(result.hasPermission()).toBe(expected);
    },
  );

  it("is false for a conditional result specifically — this exact case used to fail open to `true`", () => {
    const result = new CheckResult("conditionalPermission", ["now"], "rev-1");
    expect(result.hasPermission()).toBe(false);
  });

  it("checkResultFromProto: a CONDITIONAL_PERMISSION response yields hasPermission() === false", () => {
    const resp = create(CheckPermissionResponseSchema, {
      permissionship:
        CheckPermissionResponse_Permissionship.CONDITIONAL_PERMISSION,
      partialCaveatInfo: create(PartialCaveatInfoSchema, {
        missingRequiredContext: ["now"],
      }),
    });
    expect(checkResultFromProto(resp).hasPermission()).toBe(false);
  });
});

describe("checkResultFromProto()", () => {
  it("maps a HAS_PERMISSION response, with no missing context, and hasPermission() true", () => {
    const resp = create(CheckPermissionResponseSchema, {
      permissionship: CheckPermissionResponse_Permissionship.HAS_PERMISSION,
      checkedAt: create(ZedTokenSchema, { token: "rev-1" }),
    });
    const result = checkResultFromProto(resp);
    expect(result.permissionship).toBe("hasPermission");
    expect(result.hasPermission()).toBe(true);
    expect(result.missingContext).toEqual([]);
  });

  // T2 — asserted by exact value, not merely "non-empty".
  it("carries the server's missing_required_context contents by value (T2)", () => {
    const resp = create(CheckPermissionResponseSchema, {
      permissionship:
        CheckPermissionResponse_Permissionship.CONDITIONAL_PERMISSION,
      partialCaveatInfo: create(PartialCaveatInfoSchema, {
        missingRequiredContext: ["now", "region"],
      }),
    });
    const result = checkResultFromProto(resp);
    expect(result.missingContext).toEqual(["now", "region"]);
  });

  // T3 — the checked-at token is populated from the response.
  it("populates checkedAt from the response's checked_at token (T3)", () => {
    const resp = create(CheckPermissionResponseSchema, {
      permissionship: CheckPermissionResponse_Permissionship.HAS_PERMISSION,
      checkedAt: create(ZedTokenSchema, { token: "rev-123" }),
    });
    expect(checkResultFromProto(resp).checkedAt).toBe("rev-123");
  });

  it("maps NO_PERMISSION", () => {
    const resp = create(CheckPermissionResponseSchema, {
      permissionship: CheckPermissionResponse_Permissionship.NO_PERMISSION,
    });
    const result = checkResultFromProto(resp);
    expect(result.permissionship).toBe("noPermission");
    expect(result.hasPermission()).toBe(false);
  });

  it("defaults checkedAt to empty string when unset", () => {
    const resp = create(CheckPermissionResponseSchema, {
      permissionship: CheckPermissionResponse_Permissionship.HAS_PERMISSION,
    });
    expect(checkResultFromProto(resp).checkedAt).toBe("");
  });
});

describe("checkResultFromBulkItem()", () => {
  it("uses the passed-in responseCheckedAt — CheckBulkPermissionsResponseItem has no per-item token", () => {
    const item = create(CheckBulkPermissionsResponseItemSchema, {
      permissionship:
        CheckPermissionResponse_Permissionship.CONDITIONAL_PERMISSION,
      partialCaveatInfo: create(PartialCaveatInfoSchema, {
        missingRequiredContext: ["now"],
      }),
    });
    const result = checkResultFromBulkItem(item, "response-level-token");
    expect(result.checkedAt).toBe("response-level-token");
    expect(result.missingContext).toEqual(["now"]);
    expect(result.hasPermission()).toBe(false);
  });

  it("maps HAS_PERMISSION with hasPermission() true", () => {
    const item = create(CheckBulkPermissionsResponseItemSchema, {
      permissionship: CheckPermissionResponse_Permissionship.HAS_PERMISSION,
    });
    expect(checkResultFromBulkItem(item, "r").hasPermission()).toBe(true);
  });
});

// ---------------------------------------------------------------------------
// Client-level tests: SpiceDBClient doesn't accept a proto client via its
// public constructor, so these replace the private `proto` field at runtime
// with a fake whose methods are `vi.fn()`s we control, mirroring
// streaming-retry.test.ts's harness (adapted here for unary, not streaming,
// RPCs).
// ---------------------------------------------------------------------------

interface FakeProto {
  permissions?: Record<string, ReturnType<typeof vi.fn>>;
}

function clientWithFakeProto(fake: FakeProto): SpiceDBClient {
  const client = new SpiceDBClient({
    endpoint: "localhost:50051",
    token: "test-token",
    insecure: true,
  });
  (client as unknown as { proto: FakeProto }).proto = fake;
  return client;
}

const aCheck = (overrides: Partial<Parameters<SpiceDBClient["checkPermission"]>[1]> = {}) => ({
  resourceType: "document",
  resourceId: "1",
  permission: "view",
  subjectType: "user",
  subjectId: "jimmy",
  ...overrides,
});

describe("SpiceDBClient.checkPermission()", () => {
  it("returns a CheckResult (not a boolean), reflecting HAS_PERMISSION as a grant", async () => {
    const fn = vi.fn().mockResolvedValue(
      create(CheckPermissionResponseSchema, {
        permissionship: CheckPermissionResponse_Permissionship.HAS_PERMISSION,
        checkedAt: create(ZedTokenSchema, { token: "rev-9" }),
      }),
    );
    const client = clientWithFakeProto({
      permissions: { checkPermission: fn },
    });

    const result = await client.checkPermission(full(), aCheck());
    expect(result).toBeInstanceOf(CheckResult);
    expect(result.hasPermission()).toBe(true);
    expect(result.checkedAt).toBe("rev-9");
  });

  it("returns hasPermission()===false for a conditional response — the fail-open this task closes", async () => {
    const fn = vi.fn().mockResolvedValue(
      create(CheckPermissionResponseSchema, {
        permissionship:
          CheckPermissionResponse_Permissionship.CONDITIONAL_PERMISSION,
        partialCaveatInfo: create(PartialCaveatInfoSchema, {
          missingRequiredContext: ["now"],
        }),
        checkedAt: create(ZedTokenSchema, { token: "rev-10" }),
      }),
    );
    const client = clientWithFakeProto({
      permissions: { checkPermission: fn },
    });

    const result = await client.checkPermission(full(), aCheck());
    expect(result.permissionship).toBe("conditionalPermission");
    expect(result.hasPermission()).toBe(false);
    expect(result.missingContext).toEqual(["now"]);
  });
});

describe("SpiceDBClient.checkPermissions()", () => {
  it("propagates the response-level checkedAt onto every item", async () => {
    const fn = vi.fn().mockResolvedValue({
      checkedAt: create(ZedTokenSchema, { token: "batch-rev" }),
      pairs: [
        {
          response: {
            case: "item",
            value: create(CheckBulkPermissionsResponseItemSchema, {
              permissionship:
                CheckPermissionResponse_Permissionship.HAS_PERMISSION,
            }),
          },
        },
        {
          response: {
            case: "item",
            value: create(CheckBulkPermissionsResponseItemSchema, {
              permissionship:
                CheckPermissionResponse_Permissionship.CONDITIONAL_PERMISSION,
              partialCaveatInfo: create(PartialCaveatInfoSchema, {
                missingRequiredContext: ["now"],
              }),
            }),
          },
        },
      ],
    });
    const client = clientWithFakeProto({
      permissions: { checkBulkPermissions: fn },
    });

    const results = await client.checkPermissions(
      full(),
      aCheck({ subjectId: "a" }),
      aCheck({ subjectId: "b" }),
    );

    expect(results).toHaveLength(2);
    expect(results.every((r) => r.checkedAt === "batch-rev")).toBe(true);
    expect(results[0].hasPermission()).toBe(true);
    expect(results[1].hasPermission()).toBe(false); // conditional is NOT a grant
    expect(results[1].missingContext).toEqual(["now"]);
  });

  // -------------------------------------------------------------------------
  // HARD REQUIREMENT: a per-item CheckBulkPermissions error MUST surface as
  // a typed SpiceDBError, never as a falsy/failing CheckResult. Before this
  // fix, `pair.response.case === "error"` silently became `false` —
  // indistinguishable from a real denial.
  // -------------------------------------------------------------------------
  it("surfaces a per-item error as a typed SpiceDBError instead of a falsy result", async () => {
    const fn = vi.fn().mockResolvedValue({
      checkedAt: create(ZedTokenSchema, { token: "batch-rev" }),
      pairs: [
        {
          response: {
            case: "error",
            value: { code: 7, message: "PERMISSION_DENIED: nope" }, // 7 = PermissionDenied
          },
        },
      ],
    });
    const client = clientWithFakeProto({
      permissions: { checkBulkPermissions: fn },
    });

    await expect(
      client.checkPermissions(full(), aCheck()),
    ).rejects.toBeInstanceOf(PermissionDeniedError);
  });

  it("maps different per-item error codes to their corresponding typed errors", async () => {
    const fn = vi.fn().mockResolvedValue({
      checkedAt: create(ZedTokenSchema, { token: "batch-rev" }),
      pairs: [
        { response: { case: "error", value: { code: 5, message: "not found" } } }, // 5 = NotFound
      ],
    });
    const client = clientWithFakeProto({
      permissions: { checkBulkPermissions: fn },
    });

    await expect(
      client.checkPermissions(full(), aCheck()),
    ).rejects.toBeInstanceOf(NotFoundError);
  });
});

describe("checkAny / checkAll — conditional does not count as granted (fail-closed)", () => {
  it("checkAny returns false when the only result is conditional", async () => {
    const fn = vi.fn().mockResolvedValue({
      checkedAt: create(ZedTokenSchema, { token: "r" }),
      pairs: [
        {
          response: {
            case: "item",
            value: create(CheckBulkPermissionsResponseItemSchema, {
              permissionship:
                CheckPermissionResponse_Permissionship.CONDITIONAL_PERMISSION,
            }),
          },
        },
      ],
    });
    const client = clientWithFakeProto({
      permissions: { checkBulkPermissions: fn },
    });

    expect(await client.checkAny(full(), aCheck())).toBe(false);
  });

  it("checkAll returns false when one of several results is conditional", async () => {
    const fn = vi.fn().mockResolvedValue({
      checkedAt: create(ZedTokenSchema, { token: "r" }),
      pairs: [
        {
          response: {
            case: "item",
            value: create(CheckBulkPermissionsResponseItemSchema, {
              permissionship:
                CheckPermissionResponse_Permissionship.HAS_PERMISSION,
            }),
          },
        },
        {
          response: {
            case: "item",
            value: create(CheckBulkPermissionsResponseItemSchema, {
              permissionship:
                CheckPermissionResponse_Permissionship.CONDITIONAL_PERMISSION,
            }),
          },
        },
      ],
    });
    const client = clientWithFakeProto({
      permissions: { checkBulkPermissions: fn },
    });

    expect(
      await client.checkAll(full(), aCheck({ subjectId: "a" }), aCheck({ subjectId: "b" })),
    ).toBe(false);
  });

  it("checkAny returns true when at least one result is an unconditional grant", async () => {
    const fn = vi.fn().mockResolvedValue({
      checkedAt: create(ZedTokenSchema, { token: "r" }),
      pairs: [
        {
          response: {
            case: "item",
            value: create(CheckBulkPermissionsResponseItemSchema, {
              permissionship:
                CheckPermissionResponse_Permissionship.NO_PERMISSION,
            }),
          },
        },
        {
          response: {
            case: "item",
            value: create(CheckBulkPermissionsResponseItemSchema, {
              permissionship:
                CheckPermissionResponse_Permissionship.HAS_PERMISSION,
            }),
          },
        },
      ],
    });
    const client = clientWithFakeProto({
      permissions: { checkBulkPermissions: fn },
    });

    expect(
      await client.checkAny(full(), aCheck({ subjectId: "a" }), aCheck({ subjectId: "b" })),
    ).toBe(true);
  });
});
