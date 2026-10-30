/**
 * Root DESIGN.md, "Reject unusable credentials and trust material at
 * construction": dynamically typed callers get their names and IDs
 * type-checked before use (a wrong type is a local `InvalidArgumentError`,
 * never a server round trip with a coerced value), and a caller-supplied
 * revision token that is empty or whitespace-only is rejected like an empty one.
 */
import { afterEach, describe, expect, it, vi } from "vitest";
import { SpiceDBClient, type SpiceDBClientInterface } from "../client.js";
import { full, atLeast, snapshot, atLeastOrFull, atLeastOrMinLatency } from "../consistency.js";
import { InvalidArgumentError } from "../errors.js";
import { TransportSecurity } from "../modes.js";
import { consistencyFromProto } from "../protoconv.js";
import { InMemorySpiceDB } from "../testing.js";
import { Transaction, ZedToken, relationship, relationshipFromTuple, type CheckRequest, type Relationship, type RelationshipFilter } from "../types.js";

afterEach(() => vi.restoreAllMocks());

const BAD_VALUES: readonly unknown[] = [42, null, { id: "x" }, ["x"], true];
const bad = (value: unknown) => value as never;

const check = (): CheckRequest => ({ resource: { objectType: "document", objectId: "readme" }, permission: "view", subject: { object: { objectType: "user", objectId: "alice" } }, consistency: full() });
const rel = (): Relationship => ({ resource: { objectType: "document", objectId: "readme" }, relation: "viewer", subject: { object: { objectType: "user", objectId: "alice" } } });
const filter = (): RelationshipFilter => ({ resourceType: "document" });

/** A client whose every raw RPC is a spy, so a test can assert none was made. */
function spiedClient(): { client: SpiceDBClient; rpcs: ReturnType<typeof vi.fn>[] } {
    const client = new SpiceDBClient({ endpoint: "localhost:1", token: "t", transportSecurity: TransportSecurity.Plaintext });
    const rpcs: ReturnType<typeof vi.fn>[] = [];
    const raw = client.raw() as unknown as Record<string, Record<string, unknown>>;
    for (const service of Object.values(raw)) {
        if (service === null || typeof service !== "object")
            continue;
        for (const name of Object.keys(service)) {
            if (typeof service[name] === "function")
                rpcs.push(vi.spyOn(service as Record<string, () => unknown>, name).mockImplementation(() => { throw new Error(`RPC ${name} reached`); }) as unknown as ReturnType<typeof vi.fn>);
        }
    }
    return { client, rpcs };
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
const CASES: readonly Case[] = [
    ["check resource objectId", (c, v) => c.check({ ...check(), resource: { objectType: "document", objectId: v } })],
    ["check resource objectType", (c, v) => c.check({ ...check(), resource: { objectType: v, objectId: "readme" } })],
    ["check permission", (c, v) => c.check({ ...check(), permission: v })],
    ["check subject objectId", (c, v) => c.check({ ...check(), subject: { object: { objectType: "user", objectId: v } } })],
    ["check subject relation", (c, v) => c.check({ ...check(), subject: { object: { objectType: "user", objectId: "alice" }, relation: v } })],
    ["checkBulk resource objectId", (c, v) => c.checkBulk([check(), { ...check(), resource: { objectType: "document", objectId: v } }])],
    ["checkBulk permission", (c, v) => c.checkBulk([check(), { ...check(), permission: v }])],
    ["lookupResources resourceType", (c, v) => c.lookupResources({ resourceType: v, permission: "view", subject: check().subject, consistency: full() })],
    ["lookupResources permission", (c, v) => c.lookupResources({ resourceType: "document", permission: v, subject: check().subject, consistency: full() })],
    ["lookupResources subject objectId", (c, v) => c.lookupResources({ resourceType: "document", permission: "view", subject: { object: { objectType: "user", objectId: v } }, consistency: full() })],
    ["lookupSubjects resource objectId", (c, v) => c.lookupSubjects({ resource: { objectType: "document", objectId: v }, permission: "view", subjectType: "user", consistency: full() })],
    ["lookupSubjects subjectType", (c, v) => c.lookupSubjects({ resource: check().resource, permission: "view", subjectType: v, consistency: full() })],
    ["lookupSubjects subjectRelation", (c, v) => c.lookupSubjects({ resource: check().resource, permission: "view", subjectType: "user", subjectRelation: v, consistency: full() })],
    ["write relationship resource objectId", (c, v) => c.write(new Transaction().touch({ ...rel(), resource: { objectType: "document", objectId: v } }))],
    ["write relationship relation", (c, v) => c.write(new Transaction().create({ ...rel(), relation: v }))],
    ["write relationship subject objectId", (c, v) => c.write(new Transaction().touch({ ...rel(), subject: { object: { objectType: "user", objectId: v } } }))],
    ["write caveat name", (c, v) => c.write(new Transaction().touch({ ...rel(), caveat: { name: v } }))],
    ["readRelationships filter resourceId", (c, v) => c.readRelationships({ filter: { ...filter(), resourceId: v }, consistency: full() })],
    ["readRelationships filter relation", (c, v) => c.readRelationships({ filter: { ...filter(), relation: v }, consistency: full() })],
    ["readRelationships filter subjectId", (c, v) => c.readRelationships({ filter: { ...filter(), subject: { subjectType: "user", subjectId: v } }, consistency: full() })],
    ["deleteRelationships filter resourceType", (c, v) => c.deleteRelationships({ resourceType: v }, { limit: 10 })],
    ["deleteRelationships filter resourceIdPrefix", (c, v) => c.deleteRelationships({ ...filter(), resourceIdPrefix: v }, { limit: 10 })],
];
const CLIENT_ONLY_CASES: readonly Case[] = [
    ["expandPermissionTree resource objectId", (c, v) => c.expandPermissionTree({ resource: { objectType: "document", objectId: v }, permission: "view", consistency: full() })],
    ["watch objectTypes", (c, v) => c.watch({ objectTypes: [v] })],
    ["watch filter resourceType", (c, v) => c.watch({ relationshipFilters: [{ resourceType: v }] })],
    ["computablePermissions definitionName", (c, v) => c.computablePermissions({ definitionName: v, relationName: "viewer", consistency: full() })],
    ["reflectSchema filter definitionName", (c, v) => c.reflectSchema({ consistency: full(), filters: [{ definitionName: v }] })],
    ["experimentalCountRelationships name", (c, v) => c.experimentalCountRelationships(v)],
];

describe("names and IDs are type-checked before any RPC", () => {
    for (const [name, run] of [...CASES, ...CLIENT_ONLY_CASES]) {
        it.each(BAD_VALUES.map(v => [describeValue(v), v]))(`${name}: rejects %s locally`, async (_label, value) => {
            const { client, rpcs } = spiedClient();
            const error = await failure(() => run(client, bad(value)));
            expect(error).toBeInstanceOf(InvalidArgumentError);
            expect((error as Error).message).toMatch(/must be a string|requires objectType and objectId|requires resource, relation, and subject|is required|must be a RelationshipFilter/);
            for (const rpc of rpcs)
                expect(rpc).not.toHaveBeenCalled();
            client.close();
        });
    }
    it("names the offending field", async () => {
        const { client } = spiedClient();
        const error = await failure(() => client.check({ ...check(), resource: { objectType: "document", objectId: bad(42) } }));
        expect((error as Error).message).toBe("resource.objectId must be a string, got number");
        client.close();
    });
    it("rejects a bulk item before any chunk is sent, naming its index", async () => {
        const { client, rpcs } = spiedClient();
        const error = await failure(() => client.checkBulk([check(), { ...check(), permission: bad(7) }]));
        expect((error as Error).message).toMatch(/check item 1: permission must be a string/);
        for (const rpc of rpcs)
            expect(rpc).not.toHaveBeenCalled();
        client.close();
    });
    it("the in-memory double rejects the same inputs", async () => {
        for (const [name, run] of CASES) {
            for (const value of BAD_VALUES) {
                const error = await failure(() => run(new InMemorySpiceDB([rel()]), bad(value)));
                expect(error, `${name} with ${describeValue(value)}`).toBeInstanceOf(InvalidArgumentError);
            }
        }
    });
    it("parsers reject non-string input with InvalidArgumentError", () => {
        expect(() => relationship(bad(42), "viewer", "user:alice")).toThrow(InvalidArgumentError);
        expect(() => relationshipFromTuple(bad(null))).toThrow(InvalidArgumentError);
    });
    it("still accepts well-typed input", async () => {
        const double = new InMemorySpiceDB([rel()]);
        expect((await double.check(check())).hasPermission()).toBe(false);
        expect(() => relationship("document:readme", "viewer", "user:alice#member")).not.toThrow();
    });
});

describe("caller-supplied revision tokens", () => {
    const blanks = ["", " ", "\t\n"];
    it.each(blanks)("atLeast/snapshot reject %j", (token) => {
        expect(() => atLeast(new ZedToken(token))).toThrow(InvalidArgumentError);
        expect(() => snapshot(new ZedToken(token))).toThrow(InvalidArgumentError);
    });
    it.each(blanks)("a raw consistency with token %j is rejected on conversion", (token) => {
        const proto = atLeast(new ZedToken("t"))._toProto();
        if (proto.requirement.case === "atLeastAsFresh")
            proto.requirement.value.token = token;
        expect(() => consistencyFromProto(proto)).toThrow(InvalidArgumentError);
    });
    it.each(blanks)("watch and permission-set revisions reject %j before any RPC", async (token) => {
        const { client, rpcs } = spiedClient();
        for (const run of [
            () => client.watch({ startRevision: new ZedToken(token) }),
            () => client.experimentalWatchPermissionSets({ startRevision: new ZedToken(token) }),
            () => client.experimentalLookupPermissionSets({ revision: new ZedToken(token), pageSize: 10 }),
            () => client.experimentalDownloadPermissionSets({ revision: new ZedToken(token) }),
        ])
            expect(await failure(run)).toBeInstanceOf(InvalidArgumentError);
        for (const rpc of rpcs)
            expect(rpc).not.toHaveBeenCalled();
        client.close();
    });
    it.each(blanks)("atLeastOrFull/atLeastOrMinLatency fall back for %j, as for undefined", (token) => {
        expect(atLeastOrFull(new ZedToken(token)).toString()).toBe("full()");
        expect(atLeastOrMinLatency(new ZedToken(token)).toString()).toBe("minLatency()");
        expect(() => atLeastOrFull("t" as never)).toThrow(InvalidArgumentError);
    });
    it("accepts a token with surrounding text intact", () => {
        expect(atLeast(new ZedToken(" t ")).toString()).toBe("atLeast( t )");
    });
});

function describeValue(value: unknown): string {
    return value === null ? "null" : Array.isArray(value) ? "an array" : typeof value === "object" ? "an object" : `${typeof value} ${String(value)}`;
}
