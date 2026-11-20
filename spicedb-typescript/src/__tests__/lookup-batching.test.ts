// Response batching on lookupResources (root DESIGN.md: "Batch resource
// lookups on the wire without changing what callers iterate"): the request
// asks for batches by default, either wire form is read, results (not
// messages) drive paging, only a batch's last kept item carries a cursor, a
// total limit ends on a resume point, and the double batches the same way.
import { describe, it, expect } from "vitest";
import { create } from "@bufbuild/protobuf";
import { Code, createRouterTransport } from "@connectrpc/connect";
import { PermissionsService, LookupResourcesResponseSchema, LookupPermissionship, type LookupResourcesRequest as WireRequest } from "@spicedb/proto";
import {
    SpiceDBClient, InMemorySpiceDB, FaultStage, Retries, SpiceDBError, InvalidArgumentError, LookupBatching, LookupPermissionshipFilter, Paging,
    errorCode, full, relationship, relationshipFromTuple, type LookupResource, type LookupResourcesRequest,
} from "../index.js";
import { lookupPageBatchSize, lookupResourcesPaging } from "../types.js";

const subject = { object: { objectType: "user", objectId: "alice" } };
const request: LookupResourcesRequest = { resourceType: "document", permission: "view", subject, consistency: full() };
const HAS = LookupPermissionship.HAS_PERMISSION;
const COND = LookupPermissionship.CONDITIONAL_PERMISSION;

/**
 * A server holding `states.length` results (`r0`, `r1`, ...). It pages by the
 * request's limit and cursor and, as SpiceDB does, flushes a batch every
 * `optionalResponseBatchSize` results and at the end of the page. `batching:
 * false` models a server that predates batching and ignores the field.
 */
function server(states: readonly LookupPermissionship[], batching = true) {
    const requests: WireRequest[] = [];
    let messages = 0;
    const client = SpiceDBClient.fromTransport(createRouterTransport(router => router.service(PermissionsService, {
        async *lookupResources(req) {
            requests.push(req);
            const start = Number(req.optionalCursor?.token ?? 0);
            const end = Math.min(states.length, start + (req.optionalLimit || states.length));
            const size = batching ? req.optionalResponseBatchSize : 0;
            const result = (i: number) => ({ resourceObjectId: `r${i}`, permissionship: states[i], ...(states[i] === COND ? { partialCaveatInfo: { missingRequiredContext: ["ip"] } } : {}) });
            if (size === 0) {
                for (let i = start; i < end; i++, messages++)
                    yield create(LookupResourcesResponseSchema, { ...result(i), lookedUpAt: { token: "rev" }, afterResultCursor: { token: String(i + 1) } });
                return;
            }
            for (let i = start; i < end; i += size, messages++) {
                const last = Math.min(i + size, end);
                yield create(LookupResourcesResponseSchema, { lookedUpAt: { token: "rev" }, batched: { results: Array.from({ length: last - i }, (_, k) => result(i + k)), afterCursor: { token: String(last) } } });
            }
        },
    })), { retries: Retries.Disabled });
    return { client, requests, messages: () => messages };
}
const granted = (n: number) => Array.from({ length: n }, () => HAS);
async function drain(iterable: AsyncIterable<LookupResource>): Promise<LookupResource[]> {
    const items: LookupResource[] = [];
    for await (const item of iterable)
        items.push(item);
    return items;
}
const cursors = (items: readonly LookupResource[]) => items.map(item => item.afterResultCursor);
const ids = (items: readonly LookupResource[]) => items.map(item => item.resourceId);
async function failure(iterable: AsyncIterable<LookupResource>): Promise<{ error: unknown; items: LookupResource[] }> {
    const items: LookupResource[] = [];
    try {
        for await (const item of iterable)
            items.push(item);
    }
    catch (error) {
        return { error, items };
    }
    throw new Error("expected the lookup to fail");
}

describe("lookupResources requested batch size", () => {
    const sent = async (extra: Partial<LookupResourcesRequest>) => {
        const { client, requests } = server(granted(1));
        await drain(client.lookupResources({ ...request, ...extra }));
        client.close();
        return requests.map(r => r.optionalResponseBatchSize);
    };
    it("defaults to the page size, capped at 1,000", async () => {
        expect(await sent({})).toEqual([1000]);
        expect(await sent({ pageSize: 50 })).toEqual([50]);
        expect(await sent({ pageSize: 5000 })).toEqual([1000]);
    });
    it("defaults in single-request mode to the limit capped at 1,000, or 1,000 without one", async () => {
        expect(await sent({ paging: Paging.SingleRequest })).toEqual([1000]);
        expect(await sent({ paging: Paging.SingleRequest, limit: 7 })).toEqual([7]);
        expect(await sent({ paging: Paging.SingleRequest, limit: 5000 })).toEqual([1000]);
        // pageSize is ignored in single-request mode, for the batch size too.
        expect(await sent({ paging: Paging.SingleRequest, pageSize: 3 })).toEqual([1000]);
    });
    it("sends an explicit size as given, without clamping", async () => {
        expect(await sent({ responseBatchSize: 25 })).toEqual([25]);
        expect(await sent({ responseBatchSize: 2500 })).toEqual([2500]);
        expect(await sent({ pageSize: 10, responseBatchSize: 40 })).toEqual([40]);
    });
    it("sends none when batching is disabled", async () => {
        expect(await sent({ batching: LookupBatching.Disabled })).toEqual([0]);
        expect(await sent({ batching: LookupBatching.Automatic, pageSize: 4 })).toEqual([4]);
        // Disabled wins over an explicit size, which is ignored.
        expect(await sent({ batching: LookupBatching.Disabled, responseBatchSize: 10 })).toEqual([0]);
    });
    it("rejects an invalid size or mode before any request", () => {
        const { client, requests } = server(granted(1));
        for (const responseBatchSize of [0, -1, 1.5, Number.NaN, 2 ** 32])
            expect(() => client.lookupResources({ ...request, responseBatchSize })).toThrow(InvalidArgumentError);
        expect(() => client.lookupResources({ ...request, batching: "sometimes" as LookupBatching })).toThrow(InvalidArgumentError);
        // Disabled wins over a size, but the size is still validated.
        expect(() => client.lookupResources({ ...request, responseBatchSize: 0, batching: LookupBatching.Disabled })).toThrow(InvalidArgumentError);
        expect(requests).toHaveLength(0);
        client.close();
        const db = new InMemorySpiceDB();
        expect(() => db.lookupResources({ ...request, responseBatchSize: 0 })).toThrow(InvalidArgumentError);
        expect(() => db.lookupResources({ ...request, responseBatchSize: 0, batching: LookupBatching.Disabled })).toThrow(InvalidArgumentError);
    });
});

describe("lookupResources over batched responses", () => {
    it("yields every result in order, with its fields and the response's lookedUpAt, and a cursor only on batch-end items", async () => {
        const { client, messages } = server([HAS, COND, HAS, HAS, COND]);
        const items = await drain(client.lookupResources({ ...request, pageSize: 10, responseBatchSize: 2 }));
        expect(messages()).toBe(3);
        expect(ids(items)).toEqual(["r0", "r1", "r2", "r3", "r4"]);
        expect(items.map(item => item.permissionship)).toEqual(["hasPermission", "conditionalPermission", "hasPermission", "hasPermission", "conditionalPermission"]);
        expect(items.map(item => item.partialCaveat?.missingRequiredContext)).toEqual([undefined, ["ip"], undefined, undefined, ["ip"]]);
        expect(items.map(item => item.lookedUpAt.toString())).toEqual(Array.from({ length: 5 }, () => "rev"));
        expect(cursors(items)).toEqual([undefined, "2", undefined, "4", "5"]);
        // An absent cursor is an absent property, not an empty one.
        expect("afterResultCursor" in items[0]!).toBe(false);
        client.close();
    });
    it("pages by result count, sending each batch's cursor as the next page's", async () => {
        const { client, requests, messages } = server(granted(7));
        const items = await drain(client.lookupResources({ ...request, pageSize: 3 }));
        expect(ids(items)).toEqual(["r0", "r1", "r2", "r3", "r4", "r5", "r6"]);
        // One message per page: a full page is three results, not three messages.
        expect(messages()).toBe(3);
        expect(requests.map(r => r.optionalCursor?.token)).toEqual([undefined, "3", "6"]);
        expect(requests.map(r => [r.optionalLimit, r.optionalResponseBatchSize])).toEqual([[3, 3], [3, 3], [3, 3]]);
        // The short third page ends the read.
        expect(cursors(items)).toEqual([undefined, undefined, "3", undefined, undefined, "6", "7"]);
        client.close();
    });
    it("pages across several batches per page", async () => {
        const { client, requests } = server(granted(9));
        const items = await drain(client.lookupResources({ ...request, pageSize: 4, responseBatchSize: 3 }));
        expect(ids(items)).toHaveLength(9);
        expect(requests.map(r => r.optionalCursor?.token)).toEqual([undefined, "4", "8"]);
        expect(cursors(items)).toEqual([undefined, undefined, "3", "4", undefined, undefined, "7", "8", "9"]);
        client.close();
    });
    it("reads one-resource responses to a batched request, every item with its cursor", async () => {
        const { client, requests } = server(granted(5), false);
        const items = await drain(client.lookupResources({ ...request, pageSize: 2 }));
        expect(requests.map(r => r.optionalResponseBatchSize)).toEqual([2, 2, 2]);
        expect(requests.map(r => r.optionalCursor?.token)).toEqual([undefined, "2", "4"]);
        expect(ids(items)).toEqual(["r0", "r1", "r2", "r3", "r4"]);
        expect(cursors(items)).toEqual(["1", "2", "3", "4", "5"]);
        expect(items.map(item => item.lookedUpAt.toString())).toEqual(Array.from({ length: 5 }, () => "rev"));
        client.close();
    });
    it("with batching disabled, gets and yields per-item cursors", async () => {
        const { client, messages } = server(granted(3));
        expect(cursors(await drain(client.lookupResources({ ...request, batching: LookupBatching.Disabled })))).toEqual(["1", "2", "3"]);
        expect(messages()).toBe(3);
        client.close();
    });
    it("moves the cursor to the last kept item when the filter drops a batch's trailing results", async () => {
        const { client } = server([HAS, HAS, COND, COND, HAS, COND]);
        const items = await drain(client.lookupResources({ ...request, pageSize: 6, responseBatchSize: 3, permissionshipFilter: LookupPermissionshipFilter.HasPermissionOnly }));
        expect(ids(items)).toEqual(["r0", "r1", "r4"]);
        // r1 carries the first batch's cursor: resuming there skips only the dropped r2.
        expect(cursors(items)).toEqual([undefined, "3", "6"]);
        client.close();
    });
    it("advances paging past a batch the filter drops entirely, with no item carrying its cursor", async () => {
        const { client, requests } = server([COND, COND, HAS, COND, HAS]);
        const items = await drain(client.lookupResources({ ...request, pageSize: 2, permissionshipFilter: LookupPermissionshipFilter.HasPermissionOnly }));
        expect(ids(items)).toEqual(["r2", "r4"]);
        expect(requests.map(r => r.optionalCursor?.token)).toEqual([undefined, "2", "4"]);
        expect(cursors(items)).toEqual(["4", "5"]);
        client.close();
    });
    it("reports the latest batch cursor as resumeCursor when a later page fails", async () => {
        const states = granted(6);
        const client = SpiceDBClient.fromTransport(createRouterTransport(router => router.service(PermissionsService, {
            async *lookupResources(req) {
                const start = Number(req.optionalCursor?.token ?? 0);
                if (start > 0)
                    throw new SpiceDBError("down");
                yield create(LookupResourcesResponseSchema, { batched: { results: states.slice(0, 2).map((permissionship, i) => ({ resourceObjectId: `r${i}`, permissionship })), afterCursor: { token: "2" } } });
            },
        })), { retries: Retries.Disabled });
        const { error, items } = await failure(client.lookupResources({ ...request, pageSize: 2 }));
        expect(ids(items)).toEqual(["r0", "r1"]);
        expect((error as SpiceDBError).resumeCursor).toBe("2");
        client.close();
    });
    it("treats an empty batch as no results", async () => {
        const client = SpiceDBClient.fromTransport(createRouterTransport(router => router.service(PermissionsService, {
            async *lookupResources() {
                yield create(LookupResourcesResponseSchema, { batched: { results: [], afterCursor: { token: "0" } } });
                yield create(LookupResourcesResponseSchema, { batched: { results: [{ resourceObjectId: "a", permissionship: HAS }], afterCursor: { token: "1" } } });
            },
        })), { retries: Retries.Disabled });
        const items = await drain(client.lookupResources(request));
        expect(ids(items)).toEqual(["a"]);
        expect(cursors(items)).toEqual(["1"]);
        client.close();
    });
    it("maps a permissionship newer than the client to the safe unknown", async () => {
        const { client } = server([HAS, 99 as LookupPermissionship]);
        expect((await drain(client.lookupResources(request))).map(item => item.permissionship)).toEqual(["hasPermission", "unspecified"]);
        client.close();
    });
    it.each([
        ["a missing resource ID", { permissionship: HAS }, /no resource ID/],
        ["an unset permissionship", { resourceObjectId: "b" }, /permissionship is unset/],
    ])("fails a batch result with %s as a malformed response", async (_, bad, message) => {
        const client = SpiceDBClient.fromTransport(createRouterTransport(router => router.service(PermissionsService, {
            async *lookupResources() {
                yield create(LookupResourcesResponseSchema, { batched: { results: [{ resourceObjectId: "a", permissionship: HAS }, bad], afterCursor: { token: "2" } } });
            },
        })), { retries: Retries.Disabled });
        const { error } = await failure(client.lookupResources(request));
        expect(error).toBeInstanceOf(SpiceDBError);
        expect(errorCode(error)).toBe(Code.Internal);
        expect((error as Error).message).toMatch(message);
        expect((error as Error).message).toMatch(/client detected malformed response/);
        client.close();
    });
});

describe("lookupResources limit alignment", () => {
    const paging = (extra: Partial<LookupResourcesRequest>) => lookupResourcesPaging({ ...request, ...extra });
    it("computes each page's batch size so the limit lands on a batch boundary", () => {
        // No limit: the configured size, on every page.
        expect(lookupPageBatchSize(paging({ pageSize: 10 }), 0)).toBe(10);
        expect(lookupPageBatchSize(paging({ pageSize: 10, responseBatchSize: 4 }), 30)).toBe(4);
        // At least a page remains: unchanged.
        expect(lookupPageBatchSize(paging({ pageSize: 10, responseBatchSize: 4, limit: 25 }), 0)).toBe(4);
        expect(lookupPageBatchSize(paging({ pageSize: 10, responseBatchSize: 4, limit: 20 }), 10)).toBe(4);
        // Fewer remain than a batch holds: the batch is the remaining count.
        expect(lookupPageBatchSize(paging({ pageSize: 10, limit: 4 }), 0)).toBe(4);
        expect(lookupPageBatchSize(paging({ pageSize: 10, responseBatchSize: 5, limit: 13 }), 10)).toBe(3);
        // The remaining count is a multiple of the batch size: unchanged.
        expect(lookupPageBatchSize(paging({ pageSize: 10, responseBatchSize: 5, limit: 15 }), 10)).toBe(5);
        expect(lookupPageBatchSize(paging({ pageSize: 10, responseBatchSize: 3, limit: 6 }), 0)).toBe(3);
        // Neither: no batching for that page.
        expect(lookupPageBatchSize(paging({ pageSize: 10, responseBatchSize: 3, limit: 17 }), 10)).toBe(0);
        // A limit with the has-permission-only filter: never batched.
        expect(lookupPageBatchSize(paging({ pageSize: 10, limit: 25, permissionshipFilter: LookupPermissionshipFilter.HasPermissionOnly }), 0)).toBe(0);
        expect(lookupPageBatchSize(paging({ pageSize: 10, permissionshipFilter: LookupPermissionshipFilter.HasPermissionOnly }), 0)).toBe(10);
        // Disabled stays disabled; single-request mode sends the limit itself, so the stream ends on a batch.
        expect(lookupPageBatchSize(paging({ pageSize: 10, limit: 4, batching: LookupBatching.Disabled }), 0)).toBe(0);
        expect(lookupPageBatchSize(paging({ paging: Paging.SingleRequest, limit: 7, responseBatchSize: 3 }), 0)).toBe(3);
    });
    it.each([
        ["fewer remain than a batch", { pageSize: 10, limit: 4 }, [4]],
        ["fewer remain than a batch on a later page", { pageSize: 10, responseBatchSize: 5, limit: 13 }, [5, 3]],
        ["the remaining count is a multiple of the batch", { pageSize: 10, responseBatchSize: 5, limit: 15 }, [5, 5]],
        ["the remaining count fits no batch boundary", { pageSize: 10, responseBatchSize: 3, limit: 17 }, [3, 0]],
        ["the limit is a whole number of pages", { pageSize: 5, limit: 10 }, [5, 5]],
    ] as const)("ends a limited call on a cursor when %s", async (_, extra, sizes) => {
        const { client, requests } = server(granted(40));
        const items = await drain(client.lookupResources({ ...request, ...extra }));
        expect(items).toHaveLength(extra.limit);
        expect(requests.map(r => r.optionalResponseBatchSize)).toEqual(sizes);
        // The wire limit is the page size on every page, never the remaining count.
        expect(requests.map(r => r.optionalLimit)).toEqual(sizes.map(() => extra.pageSize));
        expect(items.at(-1)!.afterResultCursor).toBe(String(extra.limit));
        // Resuming from it continues exactly after the limit.
        const rest = await drain(client.lookupResources({ ...request, pageSize: extra.pageSize, cursor: items.at(-1)!.afterResultCursor, limit: 1 }));
        expect(ids(rest)).toEqual([`r${extra.limit}`]);
        client.close();
    });
    it("does not batch a limited call with the has-permission-only filter, so its last item carries a cursor", async () => {
        const { client, requests } = server([COND, HAS, COND, HAS, HAS, HAS]);
        const items = await drain(client.lookupResources({ ...request, pageSize: 4, limit: 2, permissionshipFilter: LookupPermissionshipFilter.HasPermissionOnly }));
        expect(ids(items)).toEqual(["r1", "r3"]);
        expect(requests.map(r => r.optionalResponseBatchSize)).toEqual([0]);
        expect(cursors(items)).toEqual(["2", "4"]);
        client.close();
    });
    it("ends a limited single-request call on a cursor", async () => {
        const { client, requests } = server(granted(40));
        const items = await drain(client.lookupResources({ ...request, paging: Paging.SingleRequest, limit: 7, responseBatchSize: 3 }));
        expect(requests.map(r => [r.optionalLimit, r.optionalResponseBatchSize])).toEqual([[7, 3]]);
        expect(cursors(items)).toEqual([undefined, undefined, "3", undefined, undefined, "6", "7"]);
        client.close();
    });
});

describe("InMemorySpiceDB lookupResources batching", () => {
    const lookup: LookupResourcesRequest = { ...request, permission: "viewer" };
    const store = (n: number) => new InMemorySpiceDB(Array.from({ length: n }, (_, i) => relationship(`document:d${i}`, "viewer", "user:alice")));
    it("puts a cursor only on the last item of each batch", async () => {
        expect(cursors(await drain(store(5).lookupResources(lookup)))).toEqual([undefined, undefined, undefined, undefined, "5"]);
        expect(cursors(await drain(store(5).lookupResources({ ...lookup, pageSize: 2 })))).toEqual([undefined, "2", undefined, "4", "5"]);
        expect(cursors(await drain(store(5).lookupResources({ ...lookup, responseBatchSize: 3 })))).toEqual([undefined, undefined, "3", undefined, "5"]);
        // A page boundary flushes the batch: pages of 4, batches of 3.
        expect(cursors(await drain(store(6).lookupResources({ ...lookup, pageSize: 4, responseBatchSize: 3 })))).toEqual([undefined, undefined, "3", "4", undefined, "6"]);
    });
    it("resumes from a batch-end cursor with exactly the rest", async () => {
        const db = store(5);
        const items = await drain(db.lookupResources({ ...lookup, pageSize: 2 }));
        const rest = await drain(db.lookupResources({ ...lookup, pageSize: 2, cursor: items[1]!.afterResultCursor }));
        expect(ids(rest)).toEqual(["d2", "d3", "d4"]);
        expect(cursors(rest)).toEqual([undefined, "4", "5"]);
    });
    it("gives per-item cursors with batching disabled", async () => {
        expect(cursors(await drain(store(3).lookupResources({ ...lookup, batching: LookupBatching.Disabled, responseBatchSize: 2 })))).toEqual(["1", "2", "3"]);
        expect(cursors(await drain(store(3).lookupResources({ ...lookup, batching: LookupBatching.Disabled })))).toEqual(["1", "2", "3"]);
    });
    it("ends a limited call on a cursor, batching as the client would request", async () => {
        const db = store(9);
        // Page 1 is a batch of 2; one result remains for page 2, so its batch is 1.
        const limited = await drain(db.lookupResources({ ...lookup, pageSize: 2, limit: 3 }));
        expect(cursors(limited)).toEqual([undefined, "2", "3"]);
        // A page of 5 in batches of 3, then the 2 that remain as one batch of 2.
        expect(cursors(await drain(db.lookupResources({ ...lookup, pageSize: 5, responseBatchSize: 3, limit: 7 })))).toEqual([undefined, undefined, "3", undefined, "5", undefined, "7"]);
        // Seven of a page of 10 fit no boundary of 3: that page is unbatched.
        expect(cursors(await drain(db.lookupResources({ ...lookup, pageSize: 10, responseBatchSize: 3, limit: 7 })))).toEqual(["1", "2", "3", "4", "5", "6", "7"]);
        expect(ids(await drain(db.lookupResources({ ...lookup, cursor: limited.at(-1)!.afterResultCursor, limit: 2 })))).toEqual(["d3", "d4"]);
    });
    it("moves a batch's cursor to the last item the filter keeps", async () => {
        const db = new InMemorySpiceDB([
            relationship("document:d1", "viewer", "user:alice"),
            relationship("document:d2", "viewer", "user:alice"),
            relationshipFromTuple("document:d3#viewer@user:alice[ip_range]"),
            relationshipFromTuple("document:d4#viewer@user:alice[ip_range]"),
            relationshipFromTuple("document:d5#viewer@user:alice[ip_range]"),
            relationship("document:d6", "viewer", "user:alice"),
        ]);
        const items = await drain(db.lookupResources({ ...lookup, pageSize: 6, responseBatchSize: 3, permissionshipFilter: LookupPermissionshipFilter.HasPermissionOnly }));
        expect(ids(items)).toEqual(["d1", "d2", "d6"]);
        expect(cursors(items)).toEqual([undefined, "3", "6"]);
        // A batch dropped entirely (d3..d5 below) leaves its cursor on no item.
        const dropped = await drain(db.lookupResources({ ...lookup, pageSize: 3, cursor: InMemorySpiceDB.cursorAfter(2), permissionshipFilter: LookupPermissionshipFilter.HasPermissionOnly }));
        expect(dropped.map(item => [item.resourceId, item.afterResultCursor])).toEqual([["d6", "6"]]);
    });
    it("reports the last batch-end cursor when a fault interrupts a batch, and resuming redelivers that batch", async () => {
        const db = store(5);
        db.setFaultHook(f => f.stage === FaultStage.Item && f.item === 3 ? InMemorySpiceDB.serverError(Code.Unavailable, "stream reset") : undefined);
        const { error, items } = await failure(db.lookupResources({ ...lookup, pageSize: 2 }));
        expect(ids(items)).toEqual(["d0", "d1", "d2"]);
        // d2 opened the second batch and carries no cursor, so the resume point is the first batch's.
        expect((error as SpiceDBError).resumeCursor).toBe("2");
        db.setFaultHook(undefined);
        expect(ids(await drain(db.lookupResources({ ...lookup, pageSize: 2, cursor: (error as SpiceDBError).resumeCursor })))).toEqual(["d2", "d3", "d4"]);
    });
});
