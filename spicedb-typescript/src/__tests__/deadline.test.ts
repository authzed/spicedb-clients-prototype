import { describe, it, expect } from "vitest";
import { createRouterTransport } from "@connectrpc/connect";
import { create } from "@bufbuild/protobuf";
import {
  SpiceDBProtoClient,
  PermissionsService,
  CheckBulkPermissionsResponseSchema,
  ReadRelationshipsResponseSchema,
  RelationshipSchema,
  ObjectReferenceSchema,
  SubjectReferenceSchema,
} from "@spicedb/proto";
import { SpiceDBClient, type SpiceDBClientOptions } from "../client.js";
import { full } from "../consistency.js";
import { DeadlineExceededError } from "../errors.js";

// ---------------------------------------------------------------------------
// Call-deadline enforcement. Root DESIGN.md, "RULE: A unary call must have a
// deadline".
//
// Uses `createRouterTransport` (from `@connectrpc/connect`) instead of a
// mocked `proto` field (the convention elsewhere in this suite): deadline
// enforcement -- `CallOptions.timeoutMs` racing an `AbortController` against
// the call, in `protocol/signals.js`'s `createDeadlineSignal` -- lives in
// Connect's own transport machinery, which a `vi.fn()` mock bypasses
// entirely. `createRouterTransport` runs the real client-side transport
// stack (the same one `createGrpcTransport` uses in production) in-process,
// against a real handler, so these tests exercise genuine enforcement
// without a real socket.
//
// Every stalling handler self-bounds its stall to STALL_MS rather than
// blocking forever: long enough to dwarf the tiny per-test deadlines below
// (proving enforcement, not luck). Each call is additionally wrapped in
// `withWatchdog`, which fails the test (instead of hanging it, and CI along
// with it) if a regression reintroduces an unbounded call.
// ---------------------------------------------------------------------------

const STALL_MS = 2000;
const WATCHDOG_MS = 10_000;

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function withWatchdog<T>(p: Promise<T>, ms = WATCHDOG_MS): Promise<T> {
  return Promise.race([
    p,
    new Promise<T>((_, reject) =>
      setTimeout(
        () =>
          reject(
            new Error(
              `call did not settle within ${ms}ms -- deadline enforcement regressed`,
            ),
          ),
        ms,
      ),
    ),
  ]);
}

function clientWithTransport(
  transport: ReturnType<typeof createRouterTransport>,
  options: Partial<SpiceDBClientOptions> = {},
): SpiceDBClient {
  const client = new SpiceDBClient({
    endpoint: "localhost:1",
    token: "test-token",
    insecure: true,
    ...options,
  });
  (client as unknown as { proto: SpiceDBProtoClient }).proto =
    new SpiceDBProtoClient(transport);
  return client;
}

const check = {
  resourceType: "document",
  resourceId: "1",
  permission: "view",
  subjectType: "user",
  subjectId: "alice",
};

describe("call deadlines", () => {
  it("fails a unary call against a stub that never responds with DeadlineExceededError, not a hang", async () => {
    const transport = createRouterTransport((router) => {
      router.service(PermissionsService, {
        checkBulkPermissions: async () => {
          await sleep(STALL_MS);
          return create(CheckBulkPermissionsResponseSchema, {});
        },
      });
    });
    const client = clientWithTransport(transport, { defaultTimeoutMs: 200 });

    const start = Date.now();
    await expect(
      withWatchdog(client.checkPermissions(full(), [check])),
    ).rejects.toBeInstanceOf(DeadlineExceededError);
    const elapsed = Date.now() - start;

    expect(elapsed).toBeLessThan(STALL_MS);
  });

  it("lets a per-call timeoutMs override a much larger client default", async () => {
    const transport = createRouterTransport((router) => {
      router.service(PermissionsService, {
        checkBulkPermissions: async () => {
          await sleep(STALL_MS);
          return create(CheckBulkPermissionsResponseSchema, {});
        },
      });
    });
    // Client default is larger than the server's stall -- if the per-call
    // override did not take effect, this call would not fail quickly.
    const client = clientWithTransport(transport, {
      defaultTimeoutMs: STALL_MS * 10,
    });

    const start = Date.now();
    await expect(
      withWatchdog(
        client.checkPermissions(full(), [check], { timeoutMs: 200 }),
      ),
    ).rejects.toBeInstanceOf(DeadlineExceededError);
    const elapsed = Date.now() - start;

    expect(elapsed).toBeLessThan(STALL_MS);
  });

  it("does not cut a streaming call off at the (tiny) unary default", async () => {
    const transport = createRouterTransport((router) => {
      router.service(PermissionsService, {
        async *readRelationships() {
          await sleep(STALL_MS);
          yield create(ReadRelationshipsResponseSchema, {
            relationship: create(RelationshipSchema, {
              resource: create(ObjectReferenceSchema, {
                objectType: "document",
                objectId: "a",
              }),
              relation: "viewer",
              subject: create(SubjectReferenceSchema, {
                object: create(ObjectReferenceSchema, {
                  objectType: "user",
                  objectId: "jimmy",
                }),
              }),
            }),
          });
        },
      });
    });
    // defaultTimeoutMs is far smaller than the server's stall. If
    // readRelationships inherited it, this would reject with
    // DeadlineExceededError instead of yielding the item.
    const client = clientWithTransport(transport, { defaultTimeoutMs: 100 });

    const start = Date.now();
    const items = await withWatchdog(
      (async () => {
        const collected = [];
        for await (const rel of client.readRelationships(
          { resourceType: "document" },
          full(),
        )) {
          collected.push(rel);
        }
        return collected;
      })(),
    );
    const elapsed = Date.now() - start;

    expect(items.map((r) => r.resourceId)).toEqual(["a"]);
    expect(elapsed).toBeGreaterThanOrEqual(STALL_MS);
  });

  describe("defaultTimeoutMs", () => {
    it("defaults to 30_000, mirroring authzed-node's DEFAULT_DEADLINE_MS", () => {
      const client = new SpiceDBClient({
        endpoint: "localhost:1",
        token: "t",
        insecure: true,
      });
      expect(
        (client as unknown as { defaultTimeoutMs: number }).defaultTimeoutMs,
      ).toBe(30_000);
    });

    it("falls back to the client default when no per-call timeoutMs is given", () => {
      const client = new SpiceDBClient({
        endpoint: "localhost:1",
        token: "t",
        insecure: true,
        defaultTimeoutMs: 7500,
      });
      const effective = (
        client as unknown as {
          effectiveTimeoutMs(timeoutMs?: number): number;
        }
      ).effectiveTimeoutMs.bind(client);
      expect(effective(undefined)).toBe(7500);
      expect(effective(1500)).toBe(1500);
    });
  });
});
