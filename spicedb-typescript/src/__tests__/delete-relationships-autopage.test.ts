import { describe, it, expect, vi } from "vitest";
import { DeleteRelationshipsResponse_DeletionProgress } from "@spicedb/proto";
import { SpiceDBClient } from "../client.js";

// ---------------------------------------------------------------------------
// deleteRelationships({ autoPage: true }) (DESIGN.md, "Deletions"): loops
// internally, threading the cursor a PARTIAL response carries into the next
// request's optionalCursor, until deletionProgress stops being PARTIAL.
// Default (autoPage unset) behavior -- a single page, unchanged -- is
// covered by src/__tests__/types.test.ts's toProtoDeleteRelationshipsRequest
// tests and unary-retry.test.ts.
// ---------------------------------------------------------------------------

interface FakeProto {
  permissions: { deleteRelationships: ReturnType<typeof vi.fn> };
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

function page(
  progress: DeleteRelationshipsResponse_DeletionProgress,
  revision: string,
  cursor?: string,
) {
  return {
    deletedAt: { token: revision },
    deletionProgress: progress,
    relationshipsDeletedCount: 1n,
    afterResultCursor: cursor ? { token: cursor } : undefined,
  };
}

describe("deleteRelationships({ autoPage: true })", () => {
  it("stops after one page when the first response is already complete", async () => {
    const fn = vi
      .fn()
      .mockResolvedValueOnce(
        page(DeleteRelationshipsResponse_DeletionProgress.COMPLETE, "rev-1"),
      );
    const client = clientWithFakeProto({ permissions: { deleteRelationships: fn } });

    const revision = await client.deleteRelationships(
      { resourceType: "document" },
      { autoPage: true },
    );

    expect(revision).toBe("rev-1");
    expect(fn).toHaveBeenCalledTimes(1);
  });

  it("loops across partial pages, threading the cursor, until complete", async () => {
    const fn = vi
      .fn()
      .mockResolvedValueOnce(
        page(
          DeleteRelationshipsResponse_DeletionProgress.PARTIAL,
          "rev-1",
          "cursor-1",
        ),
      )
      .mockResolvedValueOnce(
        page(
          DeleteRelationshipsResponse_DeletionProgress.PARTIAL,
          "rev-2",
          "cursor-2",
        ),
      )
      .mockResolvedValueOnce(
        page(DeleteRelationshipsResponse_DeletionProgress.COMPLETE, "rev-3"),
      );
    const client = clientWithFakeProto({ permissions: { deleteRelationships: fn } });

    const revision = await client.deleteRelationships(
      { resourceType: "document" },
      { limit: 20, autoPage: true },
    );

    expect(revision).toBe("rev-3");
    expect(fn).toHaveBeenCalledTimes(3);

    // First request carries no cursor; the second and third thread the
    // previous response's afterResultCursor back in as optionalCursor.
    expect(fn.mock.calls[0][0].optionalCursor).toBeUndefined();
    expect(fn.mock.calls[1][0].optionalCursor?.token).toBe("cursor-1");
    expect(fn.mock.calls[2][0].optionalCursor?.token).toBe("cursor-2");

    // Every page keeps the same page size and allows partial deletions.
    for (const call of fn.mock.calls) {
      expect(call[0].optionalLimit).toBe(20);
      expect(call[0].optionalAllowPartialDeletions).toBe(true);
    }
  });

  it("defaults the per-page size to 1000 when options.limit is not given", async () => {
    const fn = vi
      .fn()
      .mockResolvedValueOnce(
        page(DeleteRelationshipsResponse_DeletionProgress.COMPLETE, "rev-1"),
      );
    const client = clientWithFakeProto({ permissions: { deleteRelationships: fn } });

    await client.deleteRelationships(
      { resourceType: "document" },
      { autoPage: true },
    );

    expect(fn.mock.calls[0][0].optionalLimit).toBe(1000);
  });

  it("seeds the first request's optionalCursor from options.cursor, to resume an interrupted run", async () => {
    const fn = vi
      .fn()
      .mockResolvedValueOnce(
        page(DeleteRelationshipsResponse_DeletionProgress.COMPLETE, "rev-1"),
      );
    const client = clientWithFakeProto({ permissions: { deleteRelationships: fn } });

    await client.deleteRelationships(
      { resourceType: "document" },
      { autoPage: true, cursor: "resume-from-here" },
    );

    expect(fn.mock.calls[0][0].optionalCursor?.token).toBe("resume-from-here");
  });

  it("stops on an unrecognized future deletionProgress value rather than looping forever", async () => {
    const fn = vi.fn().mockResolvedValueOnce(
      page(99 as DeleteRelationshipsResponse_DeletionProgress, "rev-1"),
    );
    const client = clientWithFakeProto({ permissions: { deleteRelationships: fn } });

    const revision = await client.deleteRelationships(
      { resourceType: "document" },
      { autoPage: true },
    );

    expect(revision).toBe("rev-1");
    expect(fn).toHaveBeenCalledTimes(1);
  });
});
