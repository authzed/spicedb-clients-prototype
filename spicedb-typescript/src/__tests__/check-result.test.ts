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

// ---------------------------------------------------------------------------
// checkAll must not be vacuously true on zero checks. Array.prototype.every
// is vacuously true over an empty array, so a caller gating on
// checkAll(cs, "edit", ...docs.map(toCheck)) would have been silently
// granted whenever the derived checks array came up empty (a filter that
// matched nothing, an upstream returning []). Root DESIGN.md: "An aggregate
// over zero checks is not a grant." checkAny is deliberately left alone —
// Array.prototype.some is already correctly false on empty.
// ---------------------------------------------------------------------------
describe("checkAll — zero checks is not a grant (fail-closed)", () => {
  it("returns false for zero checks, even if the bulk-check call would have returned zero pairs", async () => {
    const fn = vi.fn().mockResolvedValue({
      checkedAt: create(ZedTokenSchema, { token: "r" }),
      pairs: [],
    });
    const client = clientWithFakeProto({
      permissions: { checkBulkPermissions: fn },
    });

    expect(await client.checkAll(full())).toBe(false);
  });
});

// ---------------------------------------------------------------------------
// Task 16 — call-level caveat context default (`CheckOptions`), and its
// merge with the pre-existing per-item context (`CheckRequest.context`).
//
// All assertions here inspect the actual request built by the client (the
// fake proto fn's captured argument), by value — not just the observed
// CheckResult behavior — so a wholesale-replacement merge implementation
// (which would silently drop shared call-level keys) cannot pass these.
// ---------------------------------------------------------------------------
describe("call-level CheckOptions — default context, per-item context, and their merge (Task 16)", () => {
  function bulkFake(count: number) {
    return vi.fn().mockResolvedValue({
      checkedAt: create(ZedTokenSchema, { token: "r" }),
      pairs: Array.from({ length: count }, () => ({
        response: {
          case: "item" as const,
          value: create(CheckBulkPermissionsResponseItemSchema, {
            permissionship: CheckPermissionResponse_Permissionship.HAS_PERMISSION,
          }),
        },
      })),
    });
  }

  // C1
  it("call-level context alone reaches every item in a bulk request", async () => {
    const fn = bulkFake(2);
    const client = clientWithFakeProto({ permissions: { checkBulkPermissions: fn } });

    await client.checkPermissions(
      full(),
      [aCheck({ subjectId: "a" }), aCheck({ subjectId: "b" })],
      { context: { now: 42 } },
    );

    const request = fn.mock.calls[0][0];
    expect(request.items).toHaveLength(2);
    expect(request.items[0].context).toEqual({ now: 42 });
    expect(request.items[1].context).toEqual({ now: 42 });
  });

  // C2 — per-item context (pre-existing) reaches only that item; also
  // confirms the classic variadic call form's request shape is unchanged.
  it("per-item context alone reaches only that item", async () => {
    const fn = bulkFake(2);
    const client = clientWithFakeProto({ permissions: { checkBulkPermissions: fn } });

    await client.checkPermissions(
      full(),
      aCheck({ subjectId: "a", context: { region: "eu" } }),
      aCheck({ subjectId: "b" }),
    );

    const request = fn.mock.calls[0][0];
    expect(request.items[0].context).toEqual({ region: "eu" });
    expect(request.items[1].context).toBeUndefined();
  });

  // C3 — THE merge rule. Both items are asserted: asserting only the
  // overriding item would also pass under wholesale-replacement semantics,
  // so a single-item assertion does not pin the rule.
  it("merges call-level default with per-item context: item wins on key conflict, call-level keys the item didn't override are retained", async () => {
    const fn = bulkFake(2);
    const client = clientWithFakeProto({ permissions: { checkBulkPermissions: fn } });

    await client.checkPermissions(
      full(),
      [
        aCheck({ subjectId: "overrides-region", context: { region: "eu" } }),
        aCheck({ subjectId: "no-override" }),
      ],
      { context: { now: 42, region: "us" } },
    );

    const request = fn.mock.calls[0][0];
    // Overriding item: its own `region` wins, but it still gets the
    // call-level `now` it never mentioned.
    expect(request.items[0].context).toEqual({ now: 42, region: "eu" });
    // Sibling that supplied no context of its own: gets the call-level
    // default untouched.
    expect(request.items[1].context).toEqual({ now: 42, region: "us" });
  });

  // C4
  it("sets no context field on the wire when neither call-level nor item-level context is supplied", async () => {
    const fn = bulkFake(1);
    const client = clientWithFakeProto({ permissions: { checkBulkPermissions: fn } });

    await client.checkPermissions(full(), aCheck());

    const request = fn.mock.calls[0][0];
    expect(request.items[0].context).toBeUndefined();
  });

  it("checkPermission (single-check surface) applies CheckOptions.context as a default, merged with the check's own context", async () => {
    const fn = vi.fn().mockResolvedValue(
      create(CheckPermissionResponseSchema, {
        permissionship: CheckPermissionResponse_Permissionship.HAS_PERMISSION,
      }),
    );
    const client = clientWithFakeProto({ permissions: { checkPermission: fn } });

    await client.checkPermission(full(), aCheck({ context: { region: "eu" } }), {
      context: { now: 42, region: "us" },
    });

    const request = fn.mock.calls[0][0];
    expect(request.context).toEqual({ now: 42, region: "eu" });
  });

  it("checkPermission with no CheckOptions and no item context sets no context field on the wire", async () => {
    const fn = vi.fn().mockResolvedValue(
      create(CheckPermissionResponseSchema, {
        permissionship: CheckPermissionResponse_Permissionship.HAS_PERMISSION,
      }),
    );
    const client = clientWithFakeProto({ permissions: { checkPermission: fn } });

    await client.checkPermission(full(), aCheck());

    const request = fn.mock.calls[0][0];
    expect(request.context).toBeUndefined();
  });

  it("checkAny accepts the array+options form and applies the call-level default to every item", async () => {
    const fn = bulkFake(2);
    const client = clientWithFakeProto({ permissions: { checkBulkPermissions: fn } });

    const result = await client.checkAny(
      full(),
      [aCheck({ subjectId: "a" }), aCheck({ subjectId: "b" })],
      { context: { now: 42 } },
    );

    expect(result).toBe(true);
    const request = fn.mock.calls[0][0];
    expect(request.items[0].context).toEqual({ now: 42 });
    expect(request.items[1].context).toEqual({ now: 42 });
  });

  it("checkAll accepts the array+options form and applies the call-level default to every item", async () => {
    const fn = bulkFake(2);
    const client = clientWithFakeProto({ permissions: { checkBulkPermissions: fn } });

    const result = await client.checkAll(
      full(),
      [aCheck({ subjectId: "a" }), aCheck({ subjectId: "b" })],
      { context: { now: 42 } },
    );

    expect(result).toBe(true);
    const request = fn.mock.calls[0][0];
    expect(request.items[0].context).toEqual({ now: 42 });
    expect(request.items[1].context).toEqual({ now: 42 });
  });

  // Binding invariant: the pre-existing variadic call sites' request shape
  // must not change. No CheckOptions supplied -> no context field, same as
  // before this task.
  it("does not change the existing variadic call sites' built request", async () => {
    const fn = bulkFake(2);
    const client = clientWithFakeProto({ permissions: { checkBulkPermissions: fn } });

    await client.checkPermissions(full(), aCheck({ subjectId: "a" }), aCheck({ subjectId: "b" }));

    const request = fn.mock.calls[0][0];
    expect(request.items).toHaveLength(2);
    expect(request.items[0].context).toBeUndefined();
    expect(request.items[1].context).toBeUndefined();
  });
});
