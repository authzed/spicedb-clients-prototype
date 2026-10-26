import "./token-equality.js";
import { ZedToken } from "../token.js";
import { TransportSecurity } from "../modes.js";
import { describe, it, expect, vi } from "vitest";
import { DeleteRelationshipsResponse_DeletionProgress } from "@spicedb/proto";
import { SpiceDBClient } from "../client.js";
// Non-atomic deletion forwards each acknowledged cursor to the next batch.
// Every batch is atomic, but the complete operation is not.
interface FakeProto {
    permissions: {
        deleteRelationships: ReturnType<typeof vi.fn>;
    };
}
function clientWithFakeProto(fake: FakeProto): SpiceDBClient {
    const client = new SpiceDBClient({
        endpoint: "localhost:50051",
        token: "test-token",
        transportSecurity: TransportSecurity.Plaintext
    });
    (client as unknown as {
        proto: FakeProto;
    }).proto = { ...fake, close() {} } as FakeProto;
    return client;
}
function page(progress: DeleteRelationshipsResponse_DeletionProgress, revision: string, cursor?: string) {
    return {
        deletedAt: { token: revision },
        deletionProgress: progress,
        relationshipsDeletedCount: 1n,
        afterResultCursor: cursor === undefined ? undefined : { token: cursor }
    };
}
describe("deleteAllRelationshipsNonAtomic()", () => {
    it("stops after one page when the first response is already complete", async () => {
        const fn = vi.fn().mockResolvedValueOnce(page(DeleteRelationshipsResponse_DeletionProgress.COMPLETE, "rev-1"));
        const client = clientWithFakeProto({ permissions: { deleteRelationships: fn } });
        const revision = await client.deleteAllRelationshipsNonAtomic({ resourceType: "document" }, {});
        expect(revision.revision.toString()).toBe("rev-1");
        expect(fn).toHaveBeenCalledTimes(1);
    });
    it("loops across partial pages, threading the cursor, until complete", async () => {
        const fn = vi.fn().mockResolvedValueOnce(page(DeleteRelationshipsResponse_DeletionProgress.PARTIAL, "rev-1", "cursor-1")).mockResolvedValueOnce(page(DeleteRelationshipsResponse_DeletionProgress.PARTIAL, "rev-2", "cursor-2")).mockResolvedValueOnce(page(DeleteRelationshipsResponse_DeletionProgress.COMPLETE, "rev-3"));
        const client = clientWithFakeProto({ permissions: { deleteRelationships: fn } });
        const revision = await client.deleteAllRelationshipsNonAtomic({ resourceType: "document" }, {
            pageSize: 20
        });
        expect(revision.revision.toString()).toBe("rev-3");
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
    it("defaults the per-page size to 1000 when options.pageSize is not given", async () => {
        const fn = vi.fn().mockResolvedValueOnce(page(DeleteRelationshipsResponse_DeletionProgress.COMPLETE, "rev-1"));
        const client = clientWithFakeProto({ permissions: { deleteRelationships: fn } });
        await client.deleteAllRelationshipsNonAtomic({ resourceType: "document" }, {});
        expect(fn.mock.calls[0][0].optionalLimit).toBe(1000);
    });
    it("seeds the first request's optionalCursor from options.cursor, to resume an interrupted run", async () => {
        const fn = vi.fn().mockResolvedValueOnce(page(DeleteRelationshipsResponse_DeletionProgress.COMPLETE, "rev-1"));
        const client = clientWithFakeProto({ permissions: { deleteRelationships: fn } });
        await client.deleteAllRelationshipsNonAtomic({ resourceType: "document" }, {
            cursor: "resume-from-here"
        });
        expect(fn.mock.calls[0][0].optionalCursor?.token).toBe("resume-from-here");
    });
    it("rejects an unrecognized future deletionProgress value with acknowledged progress", async () => {
        const fn = vi.fn().mockResolvedValueOnce(page(99 as DeleteRelationshipsResponse_DeletionProgress, "rev-1"));
        const client = clientWithFakeProto({ permissions: { deleteRelationships: fn } });
        await expect(client.deleteAllRelationshipsNonAtomic({ resourceType: "document" }, {})).rejects.toMatchObject({
            deleteProgress: { revision: new ZedToken("rev-1"), progress: "unspecified", deletedCount: 1n },
        });
        expect(fn).toHaveBeenCalledTimes(1);
    });
    it("omits an empty response cursor instead of retaining the previous cursor", async () => {
        const fn = vi.fn()
            .mockResolvedValueOnce(page(DeleteRelationshipsResponse_DeletionProgress.PARTIAL, "rev-1", "cursor-1"))
            .mockResolvedValueOnce(page(DeleteRelationshipsResponse_DeletionProgress.PARTIAL, "rev-2", ""))
            .mockResolvedValueOnce(page(DeleteRelationshipsResponse_DeletionProgress.COMPLETE, "rev-3"));
        const client = clientWithFakeProto({ permissions: { deleteRelationships: fn } });
        try {
            const result = await client.deleteAllRelationshipsNonAtomic({ resourceType: "document" }, { cursor: "" });
            expect(fn.mock.calls.map(([request]) => request.optionalCursor?.token)).toEqual([undefined, "cursor-1", undefined]);
            expect(fn.mock.calls[2][0].optionalCursor).toBeUndefined();
            expect(result.deletedCount).toBe(3n);
        } finally { client.close(); }
    });
    it("retains the last acknowledged cursor and count if the next batch fails", async () => {
        const fn = vi.fn()
            .mockResolvedValueOnce(page(DeleteRelationshipsResponse_DeletionProgress.PARTIAL, "rev-1", "resume/opaque+=="))
            .mockRejectedValueOnce(new Error("next batch failed"));
        const client = clientWithFakeProto({ permissions: { deleteRelationships: fn } });
        try {
            await expect(client.deleteAllRelationshipsNonAtomic({ resourceType: "document" })).rejects.toMatchObject({
                deleteProgress: { revision: new ZedToken("rev-1"), deletedCount: 1n, cursor: "resume/opaque+==", progress: "partial" },
            });
            expect(fn.mock.calls[1][0].optionalCursor.token).toBe("resume/opaque+==");
            expect(fn).toHaveBeenCalledTimes(2);
        } finally { client.close(); }
    });

});
