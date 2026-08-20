/**
 * Bulk-check chunking.
 *
 * SpiceDB rejects a `CheckBulkPermissions` request carrying more items than
 * `maxBulkCheckCount` -- 10,000, a hard-coded const in
 * `internal/services/v1/bulkcheck.go` with no flag to raise or lower it --
 * with `ERROR_REASON_TOO_MANY_CHECKS_IN_REQUEST`. Nothing in the proto
 * enforces this (`CheckBulkPermissionsRequest.items` carries only a
 * per-item `required` rule, not a collection-size rule), so the client is
 * what has to split large inputs.
 */
import { describe, it, expect, vi } from "vitest";
import { create } from "@bufbuild/protobuf";
import {
  CheckBulkPermissionsResponseItemSchema,
  PartialCaveatInfoSchema,
  ZedTokenSchema,
  CheckPermissionResponse_Permissionship,
} from "@spicedb/proto";
import { SpiceDBClient } from "../client.js";
import { full } from "../consistency.js";
import { SpiceDBError } from "../errors.js";

/** Mirrors the client's own `CHECK_BATCH_SIZE`, which is module-private. */
const CHECK_BATCH_SIZE = 1000;
const TOTAL = CHECK_BATCH_SIZE * 2 + 7;
const EXPECTED_SIZES = [CHECK_BATCH_SIZE, CHECK_BATCH_SIZE, 7];

/**
 * `n` checks whose resource IDs are their index, zero-padded so lexical and
 * numeric order agree when read by eye.
 */
function numberedChecks(n: number) {
  return Array.from({ length: n }, (_, i) => ({
    resourceType: "document",
    resourceId: String(i).padStart(5, "0"),
    permission: "view",
    subjectType: "user",
    subjectId: "alice",
  }));
}

interface Recorder {
  sizes: number[];
  fn: ReturnType<typeof vi.fn>;
}

/**
 * Stands in for the `checkBulkPermissions` stub. It answers every item,
 * echoing the item's resource ID back through `missingRequiredContext` so a
 * caller can prove which request item each result came from -- and therefore
 * that concatenating chunk responses preserved input order.
 *
 * `shortAtRequest`, when set, makes the request at that index (0-based)
 * return one fewer pair than it was asked for, exercising the per-chunk
 * pair-count guard.
 */
function recordingStub(
  shortAtRequest?: number,
  malformedAtAbsolute?: number,
): Recorder {
  const sizes: number[] = [];
  const fn = vi.fn(async (req: { items: { resource?: { objectId: string } }[] }) => {
    const index = sizes.length;
    const base = sizes.reduce((a, b) => a + b, 0);
    sizes.push(req.items.length);
    const items =
      shortAtRequest === index ? req.items.slice(0, -1) : req.items;
    return {
      checkedAt: create(ZedTokenSchema, { token: "tok" }),
      pairs: items.map((item, i) =>
        malformedAtAbsolute === base + i
          ? // `response` oneof left unset entirely.
            { response: { case: undefined } }
          : {
              response: {
                case: "item" as const,
                value: create(CheckBulkPermissionsResponseItemSchema, {
                  permissionship:
                    CheckPermissionResponse_Permissionship.HAS_PERMISSION,
                  partialCaveatInfo: create(PartialCaveatInfoSchema, {
                    missingRequiredContext: [item.resource?.objectId ?? ""],
                  }),
                }),
              },
            },
      ),
    };
  });
  return { sizes, fn };
}

function clientWith(recorder: Recorder): SpiceDBClient {
  const client = new SpiceDBClient({
    endpoint: "localhost:50051",
    token: "test-token",
    insecure: true,
  });
  (client as unknown as { proto: unknown }).proto = {
    permissions: { checkBulkPermissions: recorder.fn },
  };
  return client;
}

describe("checkPermissions() chunking", () => {
  it("splits an oversized input into requests of at most CHECK_BATCH_SIZE", async () => {
    const recorder = recordingStub();
    const client = clientWith(recorder);

    const results = await client.checkPermissions(
      full(),
      numberedChecks(TOTAL),
    );

    expect(results).toHaveLength(TOTAL);
    expect(recorder.sizes).toEqual(EXPECTED_SIZES);
  });

  it("keeps chunked results in input order", async () => {
    // The echo carries each item's own resource ID, so a reordering -- or a
    // chunk's results landing under the wrong offset -- is visible on every
    // one of the 2,007 results, not just at the seams.
    const recorder = recordingStub();
    const client = clientWith(recorder);

    const results = await client.checkPermissions(
      full(),
      numberedChecks(TOTAL),
    );

    expect(results.map((r) => r.missingContext[0])).toEqual(
      Array.from({ length: TOTAL }, (_, i) => String(i).padStart(5, "0")),
    );
  });

  it.each([1, 999, CHECK_BATCH_SIZE])(
    "sends exactly one request for %i checks",
    async (n) => {
      // The common case must not regress into a loop with per-chunk overhead.
      const recorder = recordingStub();
      const client = clientWith(recorder);

      const results = await client.checkPermissions(full(), numberedChecks(n));

      expect(results).toHaveLength(n);
      expect(recorder.sizes).toEqual([n]);
    },
  );

  it("sends no request at all for an empty input", async () => {
    // Zero checks costs zero round trips -- not one request carrying an
    // empty item list -- and resolves to [] rather than throwing.
    const recorder = recordingStub();
    const client = clientWith(recorder);

    await expect(client.checkPermissions(full(), [])).resolves.toEqual([]);
    expect(recorder.sizes).toEqual([]);
  });

  it("checkAll on an empty input is false and sends no request", async () => {
    // Chunking must not resurrect the vacuous-true bug: an aggregate over
    // zero checks is false, and it costs no request.
    const recorder = recordingStub();
    const client = clientWith(recorder);

    await expect(client.checkAll(full(), [])).resolves.toBe(false);
    expect(recorder.sizes).toEqual([]);
  });

  it("checkAny on an empty input is false and sends no request", async () => {
    const recorder = recordingStub();
    const client = clientWith(recorder);

    await expect(client.checkAny(full(), [])).resolves.toBe(false);
    expect(recorder.sizes).toEqual([]);
  });

  it("fires the pair-count guard on a later chunk, not just the first", async () => {
    // The guard is evaluated per chunk, not once against the caller's total:
    // the first chunk answers in full, the second returns 999 pairs for
    // 1,000 items. Without a per-chunk guard the shortfall would silently
    // desync every result from the second chunk onward.
    const recorder = recordingStub(1);
    const client = clientWith(recorder);

    const rejection = client.checkPermissions(full(), numberedChecks(TOTAL));

    await expect(rejection).rejects.toBeInstanceOf(SpiceDBError);
    await expect(rejection).rejects.toThrow(
      /999 pair\(s\) for 1000 request item\(s\)/,
    );
    // Two requests went out before the guard fired -- proof the failure was
    // detected on the second chunk, not on the whole input up front.
    expect(recorder.sizes).toEqual([CHECK_BATCH_SIZE, CHECK_BATCH_SIZE]);
  });

  it("reports the caller's absolute index in a per-item message, not the chunk-relative one", async () => {
    // Chunking made every "check item N" message chunk-relative: a failure at
    // check 1003 read as "check item 3", so a caller who logs or parses it
    // acts on check 3 — one resource's answer attributed to another, the same
    // failure family the pair-count guard exists to prevent, relocated into
    // the diagnostic.
    const failing = CHECK_BATCH_SIZE + 3;
    const recorder = recordingStub(undefined, failing);
    const client = clientWith(recorder);

    const rejection = client.checkPermissions(
      full(),
      numberedChecks(CHECK_BATCH_SIZE * 2),
    );

    await expect(rejection).rejects.toThrow(
      new RegExp(`check item ${failing}: malformed`),
    );
    await expect(rejection).rejects.not.toThrow(/check item 3: malformed/);
  });
});
