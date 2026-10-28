import { describe, it, expect, vi } from "vitest";
import { create } from "@bufbuild/protobuf";
import { LookupResourcesResponseSchema, LookupPermissionship } from "@spicedb/proto";
import { SpiceDBClient, TransportSecurity, full, LookupPermissionshipFilter, InvalidArgumentError } from "../index.js";

const request = { resourceType: "document", permission: "view", subject: { object: { objectType: "user", objectId: "alice" } }, consistency: full() };
const states = [LookupPermissionship.CONDITIONAL_PERMISSION, LookupPermissionship.UNSPECIFIED, LookupPermissionship.HAS_PERMISSION, 99 as LookupPermissionship, LookupPermissionship.HAS_PERMISSION];
function setup() {
    const client = new SpiceDBClient({ endpoint: "localhost:1", token: "secret", transportSecurity: TransportSecurity.Plaintext });
    let closed = 0;
    const rpc = vi.spyOn(client.raw().permissions, "lookupResources").mockImplementation(async function* (wire) {
        const start = Number(wire.optionalCursor?.token ?? 0);
        try {
            for (let i = start; i < Math.min(states.length, start + (wire.optionalLimit ?? states.length)); i++) {
                yield create(LookupResourcesResponseSchema, { resourceObjectId: String(i), permissionship: states[i], afterResultCursor: { token: String(i + 1) } });
            }
        } finally { closed++; }
    });
    return { client, rpc, closed: () => closed };
}
async function ids(results: AsyncIterable<{ resourceId: string }>) {
    const values: string[] = [];
    for await (const result of results) values.push(result.resourceId);
    return values;
}
describe("lookup permissionship filtering", () => {
    it("retains conditional and unknown results by default", async () => {
        const { client } = setup();
        try { expect(await ids(client.lookupResources(request))).toEqual(["0", "1", "2", "3", "4"]); }
        finally { client.close(); }
    });
    it("counts only grants toward limits and advances through all-filtered pages", async () => {
        const { client, rpc, closed } = setup();
        try {
            expect(await ids(client.lookupResources({ ...request, pageSize: 2, limit: 2, permissionshipFilter: LookupPermissionshipFilter.HasPermissionOnly }))).toEqual(["2", "4"]);
            expect(rpc.mock.calls.map(([r]) => r.optionalCursor?.token)).toEqual([undefined, "2", "4"]);
            // One fixed page size: SpiceDB hashes the limit into the cursor.
            expect(rpc.mock.calls.map(([r]) => r.optionalLimit)).toEqual([2, 2, 2]);
            expect(closed()).toBe(3);
        } finally { client.close(); }
    });
    it("rejects unknown filter values before transport", () => {
        const { client, rpc } = setup();
        try {
            expect(() => client.lookupResources({ ...request, permissionshipFilter: "future" as LookupPermissionshipFilter })).toThrow(InvalidArgumentError);
            expect(rpc).not.toHaveBeenCalled();
        } finally { client.close(); }
    });
    it("closes the underlying stream on early consumer exit", async () => {
        const { client, closed } = setup();
        try {
            for await (const result of client.lookupResources({ ...request, permissionshipFilter: LookupPermissionshipFilter.HasPermissionOnly })) {
                expect(result.resourceId).toBe("2");
                break;
            }
            expect(closed()).toBe(1);
        } finally { client.close(); }
    });
});
