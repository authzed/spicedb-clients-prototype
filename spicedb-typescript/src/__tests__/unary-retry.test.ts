import { describe, it, expect, vi } from "vitest";
import { ConnectError, Code } from "@connectrpc/connect";
import { SpiceDBClient } from "../client.js";
import { full } from "../consistency.js";
import { Transaction, relationship } from "../types.js";
import { UnavailableError, ResourceExhaustedError } from "../errors.js";

// ---------------------------------------------------------------------------
// Unary retry safety (DESIGN.md: "Automatic retry is for idempotent
// operations only").
//
// Reads retry on a transient error; mutations (write, deleteRelationships,
// writeSchema, importBulkRelationships, the counter register/unregister
// calls) do not, regardless of retryable-ness -- a WriteRelationships may
// carry OPERATION_CREATE or preconditions, and if it actually committed but
// the response was lost, a retry would surface
// ALREADY_EXISTS/FAILED_PRECONDITION for a write that in fact succeeded.
// RESOURCE_EXHAUSTED is never retried at all: in SpiceDB it means memory
// load-shed or a deterministic MaxDepthExceeded, never a transient hiccup.
//
// See src/__tests__/streaming-retry.test.ts for the same guarantees on
// streaming RPC establishment.
// ---------------------------------------------------------------------------

interface FakeProto {
  permissions?: Record<string, ReturnType<typeof vi.fn>>;
  schema?: Record<string, ReturnType<typeof vi.fn>>;
  experimental?: Record<string, ReturnType<typeof vi.fn>>;
}

function clientWithFakeProto(
  fake: FakeProto,
  maxRetries = 3,
): SpiceDBClient {
  const client = new SpiceDBClient({
    endpoint: "localhost:50051",
    token: "test-token",
    insecure: true,
    maxRetries,
  });
  (client as unknown as { proto: FakeProto }).proto = fake;
  return client;
}

const transientErr = () => new ConnectError("unavailable", Code.Unavailable);
const resourceExhaustedErr = () =>
  new ConnectError("quota", Code.ResourceExhausted);

describe("unary read retry", () => {
  it("retries readSchema on a transient error, then succeeds", async () => {
    const fn = vi
      .fn()
      .mockRejectedValueOnce(transientErr())
      .mockResolvedValueOnce({ schemaText: "definition user {}" });
    const client = clientWithFakeProto({ schema: { readSchema: fn } });

    const result = await client.readSchema();

    expect(result.schema).toBe("definition user {}");
    expect(fn).toHaveBeenCalledTimes(2);
  });

  it("never retries RESOURCE_EXHAUSTED", async () => {
    const fn = vi.fn().mockRejectedValue(resourceExhaustedErr());
    const client = clientWithFakeProto({ schema: { readSchema: fn } });

    await expect(client.readSchema()).rejects.toBeInstanceOf(
      ResourceExhaustedError,
    );
    expect(fn).toHaveBeenCalledTimes(1);
  });
});

describe("mutation retry safety", () => {
  it("attempts write() exactly once on a retryable (transient) error", async () => {
    const fn = vi.fn().mockRejectedValue(transientErr());
    const client = clientWithFakeProto({
      permissions: { writeRelationships: fn },
    });

    const txn = new Transaction().create(
      relationship("document:readme", "viewer", "user:jimmy"),
    );

    await expect(client.write(txn)).rejects.toBeInstanceOf(UnavailableError);
    expect(fn).toHaveBeenCalledTimes(1);
  });

  it("attempts deleteRelationships() exactly once on a retryable error", async () => {
    const fn = vi.fn().mockRejectedValue(transientErr());
    const client = clientWithFakeProto({
      permissions: { deleteRelationships: fn },
    });

    await expect(
      client.deleteRelationships({ resourceType: "document" }),
    ).rejects.toBeInstanceOf(UnavailableError);
    expect(fn).toHaveBeenCalledTimes(1);
  });

  it("attempts writeSchema() exactly once on a retryable error", async () => {
    const fn = vi.fn().mockRejectedValue(transientErr());
    const client = clientWithFakeProto({ schema: { writeSchema: fn } });

    await expect(
      client.writeSchema("definition user {}"),
    ).rejects.toBeInstanceOf(UnavailableError);
    expect(fn).toHaveBeenCalledTimes(1);
  });

  it("attempts importBulkRelationships() exactly once on a retryable error", async () => {
    const fn = vi.fn().mockRejectedValue(transientErr());
    const client = clientWithFakeProto({
      permissions: { importBulkRelationships: fn },
    });

    await expect(
      client.importBulkRelationships([
        relationship("document:readme", "viewer", "user:jimmy"),
      ]),
    ).rejects.toBeInstanceOf(UnavailableError);
    expect(fn).toHaveBeenCalledTimes(1);
  });

  it("attempts experimentalRegisterRelationshipCounter() exactly once on a retryable error", async () => {
    const fn = vi.fn().mockRejectedValue(transientErr());
    const client = clientWithFakeProto({
      experimental: { experimentalRegisterRelationshipCounter: fn },
    });

    await expect(
      client.experimentalRegisterRelationshipCounter("mycounter", {
        resourceType: "document",
      }),
    ).rejects.toBeInstanceOf(UnavailableError);
    expect(fn).toHaveBeenCalledTimes(1);
  });

  it("attempts experimentalUnregisterRelationshipCounter() exactly once on a retryable error", async () => {
    const fn = vi.fn().mockRejectedValue(transientErr());
    const client = clientWithFakeProto({
      experimental: { experimentalUnregisterRelationshipCounter: fn },
    });

    await expect(
      client.experimentalUnregisterRelationshipCounter("mycounter"),
    ).rejects.toBeInstanceOf(UnavailableError);
    expect(fn).toHaveBeenCalledTimes(1);
  });
});

describe("backoff jitter", () => {
  it("varies between calls instead of following a fixed schedule", () => {
    const client = clientWithFakeProto({});
    const backoffMs = (
      client as unknown as { backoffMs(attempt: number): number }
    ).backoffMs.bind(client);

    const seen = new Set<number>();
    for (let i = 0; i < 50; i++) {
      seen.add(backoffMs(2));
    }

    expect(seen.size).toBeGreaterThan(1);
    for (const v of seen) {
      expect(v).toBeGreaterThanOrEqual(0);
      expect(v).toBeLessThanOrEqual(400);
    }
  });
});
