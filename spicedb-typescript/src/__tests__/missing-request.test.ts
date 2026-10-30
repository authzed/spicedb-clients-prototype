/**
 * A JavaScript caller can pass `undefined` or `null` where a request, a bulk
 * element or an import element belongs. Every public operation of the native
 * client and of the in-memory double rejects that locally with
 * `InvalidArgumentError`, never a raw `TypeError` (or a `SpiceDBError`
 * wrapping one), and the native client makes no RPC.
 */
import { afterEach, describe, expect, it, vi } from "vitest";
import { SpiceDBClient, type SpiceDBClientInterface } from "../client.js";
import { full } from "../consistency.js";
import { InvalidArgumentError } from "../errors.js";
import { TransportSecurity } from "../modes.js";
import { InMemorySpiceDB } from "../testing.js";
import type { CheckRequest, RelationshipFilter } from "../types.js";

afterEach(() => vi.restoreAllMocks());

const check = (): CheckRequest => ({ resource: { objectType: "document", objectId: "readme" }, permission: "view", subject: { object: { objectType: "user", objectId: "alice" } }, consistency: full() });
const filter = (): RelationshipFilter => ({ resourceType: "document" });
const missing = (value: unknown) => value as never;

function spiedClient(): { client: SpiceDBClient; rpcs: ReturnType<typeof vi.fn>[]; sent: unknown[] } {
    const client = new SpiceDBClient({ endpoint: "localhost:1", token: "t", transportSecurity: TransportSecurity.Plaintext });
    const rpcs: ReturnType<typeof vi.fn>[] = [];
    const raw = client.raw() as unknown as Record<string, Record<string, unknown>>;
    for (const service of Object.values(raw)) {
        if (service === null || typeof service !== "object")
            continue;
        for (const name of Object.keys(service)) {
            if (typeof service[name] === "function" && name !== "importBulkRelationships")
                rpcs.push(vi.spyOn(service as Record<string, () => unknown>, name).mockImplementation(() => { throw new Error(`RPC ${name} reached`); }) as unknown as ReturnType<typeof vi.fn>);
        }
    }
    // A client-streaming import opens its stream before reading the caller's
    // elements, so this fake reads the request stream (where an element
    // check fails) and records what would have reached the server.
    const sent: unknown[] = [];
    vi.spyOn(client.raw().permissions, "importBulkRelationships").mockImplementation(async (input) => {
        for await (const request of input)
            sent.push(request);
        throw new Error("RPC importBulkRelationships reached");
    });
    return { client, rpcs, sent };
}

/** Runs a unary or streaming call and returns what it threw or rejected with. */
async function failure(run: () => unknown): Promise<unknown> {
    try {
        const result = run();
        if (result && typeof (result as AsyncIterator<unknown>).next === "function")
            await (result as AsyncIterator<unknown>).next();
        else
            await result;
    }
    catch (error) {
        return error;
    }
    return undefined;
}

type Case = readonly [string, (c: SpiceDBClientInterface, v: never) => unknown];
/** Every public operation, with the missing value in its request (or element) position. */
const REQUIRED: readonly Case[] = [
    ["check", (c, v) => c.check(v)],
    ["checkBulk", (c, v) => c.checkBulk(v)],
    ["checkBulk element", (c, v) => c.checkBulk([v])],
    ["checkBulk later element", (c, v) => c.checkBulk([check(), v])],
    ["write", (c, v) => c.write(v)],
    ["readRelationships", (c, v) => c.readRelationships(v)],
    ["deleteRelationships filter", (c, v) => c.deleteRelationships(v, { limit: 10 })],
    ["deleteRelationships options", (c, v) => c.deleteRelationships(filter(), v)],
    ["deleteAllRelationshipsNonAtomic", (c, v) => c.deleteAllRelationshipsNonAtomic(v)],
    ["lookupResources", (c, v) => c.lookupResources(v)],
    ["lookupSubjects", (c, v) => c.lookupSubjects(v)],
    ["expandPermissionTree", (c, v) => c.expandPermissionTree(v)],
    ["importRelationships", (c, v) => c.importRelationships(v)],
    ["importRelationships element", (c, v) => c.importRelationships([v])],
    ["exportRelationships", (c, v) => c.exportRelationships(v)],
    ["writeSchema", (c, v) => c.writeSchema(v)],
    ["reflectSchema", (c, v) => c.reflectSchema(v)],
    ["reflectSchema filter element", (c, v) => c.reflectSchema({ consistency: full(), filters: [v] })],
    ["computablePermissions", (c, v) => c.computablePermissions(v)],
    ["dependentRelations", (c, v) => c.dependentRelations(v)],
    ["diffSchema", (c, v) => c.diffSchema(v)],
    ["experimentalRegisterRelationshipCounter name", (c, v) => c.experimentalRegisterRelationshipCounter(v, filter())],
    ["experimentalRegisterRelationshipCounter filter", (c, v) => c.experimentalRegisterRelationshipCounter("counter", v)],
    ["experimentalCountRelationships", (c, v) => c.experimentalCountRelationships(v)],
    ["experimentalUnregisterRelationshipCounter", (c, v) => c.experimentalUnregisterRelationshipCounter(v)],
    ["experimentalCountRelationshipsByFilter", (c, v) => c.experimentalCountRelationshipsByFilter(v)],
    ["experimentalLookupPermissionSets", (c, v) => c.experimentalLookupPermissionSets(v)],
    ["experimentalRoaringLookupResources", (c, v) => c.experimentalRoaringLookupResources(v)],
    ["watch relationshipFilters element", (c, v) => c.watch({ relationshipFilters: [v] })],
];
/** Operations whose request is optional: a missing one is the default request, not an error. */
const OPTIONAL: readonly Case[] = [
    ["watch", (c, v) => c.watch(v)],
    ["experimentalWatchPermissionSets", (c, v) => c.experimentalWatchPermissionSets(v)],
    ["experimentalDownloadPermissionSets", (c, v) => c.experimentalDownloadPermissionSets(v)],
];

function expectLocalRejection(error: unknown, label: string): void {
    expect(error, label).toBeInstanceOf(InvalidArgumentError);
    expect((error as Error).cause, label).not.toBeInstanceOf(TypeError);
}

describe.each([undefined, null])("a missing request (%s)", (value) => {
    it.each(REQUIRED)("SpiceDBClient.%s rejects it with InvalidArgumentError before any RPC", async (_name, call) => {
        const { client, rpcs, sent } = spiedClient();
        try {
            expectLocalRejection(await failure(() => call(client, missing(value))), _name);
            for (const rpc of rpcs)
                expect(rpc).not.toHaveBeenCalled();
            expect(sent).toEqual([]);
        }
        finally {
            client.close();
        }
    });

    it.each(REQUIRED)("InMemorySpiceDB.%s rejects it with InvalidArgumentError", async (_name, call) => {
        expectLocalRejection(await failure(() => call(new InMemorySpiceDB(), missing(value))), _name);
    });

    it.each(OPTIONAL)("SpiceDBClient.%s treats it as the default request", async (_name, call) => {
        const { client, rpcs } = spiedClient();
        try {
            const error = await failure(() => call(client, missing(value)));
            expect(error).not.toBeInstanceOf(InvalidArgumentError);
            expect(error).not.toBeInstanceOf(TypeError);
            expect(rpcs.some(rpc => rpc.mock.calls.length > 0)).toBe(true);
        }
        finally {
            client.close();
        }
    });

    it.each(OPTIONAL)("InMemorySpiceDB.%s treats it as the default request", async (_name, call) => {
        const error = await failure(() => call(new InMemorySpiceDB(), missing(value)));
        expect(error).not.toBeInstanceOf(InvalidArgumentError);
        expect(error).not.toBeInstanceOf(TypeError);
    });
});

it("names the missing bulk element by its index", async () => {
    const { client } = spiedClient();
    try {
        const error = await failure(() => client.checkBulk([check(), missing(undefined)]));
        expect((error as Error).message).toContain("check item 1: request is required");
    }
    finally {
        client.close();
    }
    const doubled = await failure(() => new InMemorySpiceDB().checkBulk([check(), missing(undefined)]));
    expect((doubled as Error).message).toContain("check item 1: request is required");
});
