import { describe, it, expect } from "vitest";
import { createRouterTransport } from "@connectrpc/connect";
import { create } from "@bufbuild/protobuf";
import {
  SpiceDBProtoClient,
  PermissionsService,
  ImportBulkRelationshipsResponseSchema,
} from "@spicedb/proto";
import { SpiceDBClient } from "../client.js";
import type { Relationship } from "../types.js";

// ---------------------------------------------------------------------------
// importBulkRelationships must consume its input lazily.
//
// The point of a bulk import is volume, so the one method most likely to be
// handed a dataset larger than memory must not require the caller to
// materialize it first. Every other SpiceDB client takes a lazy sequence
// here (Go `iter.Seq`, C# `IAsyncEnumerable`, Java `Iterable`, Python
// `Iterable`, Ruby Enumerable, Rust `IntoIterator`); this client took
// `Relationship[]` and then `.map`ped it, holding the dataset twice.
//
// These tests assert on *when* the caller's sequence is pulled, not just on
// the result. A test that only checked `numLoaded` passes identically
// against an implementation that buffers everything first, which is exactly
// the regression worth guarding.
//
// `createRouterTransport` runs the real client-side transport stack
// in-process against a real handler, so the request stream really is a
// stream: the handler's `for await` pulls messages the client's generator
// produces, rather than reading a pre-built array.
// ---------------------------------------------------------------------------

const BATCH_SIZE = 1000;

function relationship(id: number): Relationship {
  return {
    resourceType: "document",
    resourceId: `doc${id}`,
    resourceRelation: "viewer",
    subjectType: "user",
    subjectId: "alice",
  };
}

function clientWithTransport(
  transport: ReturnType<typeof createRouterTransport>,
): SpiceDBClient {
  const client = new SpiceDBClient({
    endpoint: "localhost:1",
    token: "test-token",
    insecure: true,
  });
  (client as unknown as { proto: SpiceDBProtoClient }).proto =
    new SpiceDBProtoClient(transport);
  return client;
}

/** Records the size of every request message the server received. */
function recordingTransport(onFirstBatch?: () => void) {
  const batchSizes: number[] = [];
  const transport = createRouterTransport((router) => {
    router.service(PermissionsService, {
      importBulkRelationships: async (reqs) => {
        let numLoaded = 0n;
        for await (const req of reqs) {
          if (batchSizes.length === 0) {
            onFirstBatch?.();
          }
          batchSizes.push(req.relationships.length);
          numLoaded += BigInt(req.relationships.length);
        }
        return create(ImportBulkRelationshipsResponseSchema, { numLoaded });
      },
    });
  });
  return { transport, batchSizes };
}

describe("importBulkRelationships streaming", () => {
  it("sends the first batch before the caller's generator is exhausted", async () => {
    // 2.5 batches' worth. If the client materialized the sequence, the
    // generator would be fully drained before a single byte went out, and
    // `producedWhenFirstBatchArrived` would be the whole dataset.
    const total = BATCH_SIZE * 2 + 500;
    let produced = 0;
    let producedWhenFirstBatchArrived = -1;

    function* lazyRelationships(): Generator<Relationship> {
      for (let i = 0; i < total; i++) {
        produced++;
        yield relationship(i);
      }
    }

    const { transport, batchSizes } = recordingTransport(() => {
      producedWhenFirstBatchArrived = produced;
    });
    const client = clientWithTransport(transport);

    const numLoaded = await client.importBulkRelationships(lazyRelationships());

    expect(numLoaded).toBe(BigInt(total));
    expect(batchSizes).toEqual([BATCH_SIZE, BATCH_SIZE, 500]);
    expect(producedWhenFirstBatchArrived).toBeGreaterThan(0);
    expect(producedWhenFirstBatchArrived).toBeLessThan(total);
  });

  it("accepts an async iterable, so a caller can stream from a cursor", async () => {
    const total = BATCH_SIZE + 1;

    async function* fromCursor(): AsyncGenerator<Relationship> {
      for (let i = 0; i < total; i++) {
        // Stand in for awaiting a DB page / file read.
        await Promise.resolve();
        yield relationship(i);
      }
    }

    const { transport, batchSizes } = recordingTransport();
    const client = clientWithTransport(transport);

    const numLoaded = await client.importBulkRelationships(fromCursor());

    expect(numLoaded).toBe(BigInt(total));
    expect(batchSizes).toEqual([BATCH_SIZE, 1]);
  });

  it("still accepts a plain array unchanged", async () => {
    // The pre-existing call shape. Arrays are iterable, so widening the
    // parameter must not have cost the common case anything.
    const rels = [relationship(1), relationship(2), relationship(3)];

    const { transport, batchSizes } = recordingTransport();
    const client = clientWithTransport(transport);

    const numLoaded = await client.importBulkRelationships(rels);

    expect(numLoaded).toBe(3n);
    expect(batchSizes).toEqual([3]);
  });

  it("sends nothing but still completes for an empty sequence", async () => {
    const { transport, batchSizes } = recordingTransport();
    const client = clientWithTransport(transport);

    const numLoaded = await client.importBulkRelationships([]);

    expect(numLoaded).toBe(0n);
    expect(batchSizes).toEqual([]);
  });
});
