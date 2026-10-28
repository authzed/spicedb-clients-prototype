/**
 * Example: tooling-grade features (the zed adoption items)
 *
 * Everything a CLI or backup tool needs without the raw protocol client:
 *
 *   - a client over a caller-owned Connect transport (`fromTransport`),
 *   - per-call headers and response header/trailer capture,
 *   - paged reads and lookups that cross several pages, with per-item cursors,
 *     read-at revisions and resume,
 *   - imports that report the server's ALREADY_EXISTS on a duplicate, with a
 *     configurable batch size,
 *   - bulk checks that return per-item errors alongside other results,
 *   - all-or-nothing deletes and delete-all progress,
 *   - watch schema-change events,
 *   - SpiceDB's full tuple syntax, round-tripped through the server, and
 *   - native errors interoperating with Connect's `Code`.
 *
 * Every step asserts its behavior, so removing any feature fails the example.
 */
import { createGrpcTransport } from "@connectrpc/connect-node";
import {
    SpiceDBClient, createSpiceDBClient, TransportSecurity, Transaction, relationship, relationshipFromTuple, relationshipToString,
    full, atLeast, Code, errorCode, toConnectError, AlreadyExistsError, InvalidArgumentError, BulkItemErrors, DeletePartial, WatchContent,
    DefaultDeadline, Retries, protoconv, type DeleteResult,
} from "../../src/index.js";

function assert(condition: boolean, message: string): void {
    if (!condition) {
        console.error(`ASSERTION FAILED: ${message}`);
        process.exit(1);
    }
}
const endpoint = process.env.SPICEDB_ENDPOINT || "localhost:50051";
const token = process.env.SPICEDB_TOKEN || "testtoken";
const client = createSpiceDBClient(endpoint, token, { transportSecurity: TransportSecurity.Plaintext });

const schema = `
use expiration

caveat on_network(cidr string) {
  cidr == "10.0.0.0/8"
}

definition user {}

definition team {
  relation member: user
}

definition document {
  relation viewer: user | user with on_network | user with expiration | team#member
  relation editor: user
  relation owner: user
  permission view = viewer + editor + owner
  permission edit = editor + owner
  permission delete = owner
}
`;

// --- Watch schema changes (started first so the schema write is observed) ---
// An unchanged schema write is not a change, so first write a variant that the
// watched write below then really changes.
await client.writeSchema(`${schema}\ndefinition tooling_marker {}\n`);
const watchAbort = new AbortController();
const watchStart = (await client.write(new Transaction().touch(relationship("document:watch-seed", "viewer", "user:seed")))).revision;
const schemaEvent = (async () => {
    for await (const event of client.watch({ startRevision: watchStart, content: [WatchContent.Relationships, WatchContent.Schema, WatchContent.Checkpoints] }, { signal: watchAbort.signal })) {
        if (event.schemaUpdated)
            return true;
    }
    return false;
})().catch(() => false);
await client.writeSchema(schema);
const sawSchema = await Promise.race([schemaEvent, new Promise<boolean>(r => setTimeout(() => r(false), 15000).unref())]);
watchAbort.abort();
assert(sawSchema, "watch with WatchContent.Schema reports schemaUpdated after writeSchema");
console.log("watch: schema change observed");

await client.deleteAllRelationshipsNonAtomic({ resourceType: "document" });

// --- A client over a caller-owned transport ---
const transport = createGrpcTransport({
    baseUrl: `http://${endpoint}`,
    interceptors: [next => req => { req.header.set("authorization", `Bearer ${token}`); return next(req); }],
});
const borrowed = SpiceDBClient.fromTransport(transport, { defaultDeadline: DefaultDeadline.Disabled, retries: Retries.Disabled });
assert((await borrowed.readSchema()).schema.includes("definition document"), "fromTransport client reads the schema");
borrowed.close();
const again = SpiceDBClient.fromTransport(transport);
assert((await again.readSchema()).revision.toString() !== "", "closing one client leaves the caller's transport usable");
again.close();
console.log("fromTransport: caller-owned transport works and survives close()");

// --- Transport escape hatch: request headers and response trailers ---
let trailerKeys: string[] = [];
await client.readSchema({ headers: { "x-request-id": "tooling-example" }, onTrailer: t => { trailerKeys = [...t.keys()]; } });
assert(trailerKeys.some(k => k.startsWith("io.spicedb.respmeta")), `trailers captured: ${trailerKeys}`);
console.log("response trailers:", trailerKeys.join(", "));

// --- Full tuple syntax, round-tripped through the server ---
const expiration = new Date(Date.UTC(2100, 0, 2, 3, 4, 5, 120));
const tuples = [
    "document:tuple#viewer@user:alice[on_network:{\"cidr\":\"10.0.0.0/8\"}]",
    `document:tuple#viewer@user:bob[expiration:${expiration.toISOString().replace(".120Z", ".12Z")}]`,
    "document:tuple#viewer@team:eng#member",
];
await client.write(tuples.reduce((txn, t) => txn.touch(relationshipFromTuple(t)), new Transaction()));
const readBack: string[] = [];
for await (const rel of client.readRelationships({ filter: { resourceType: "document", resourceId: "tuple" }, consistency: full() }))
    readBack.push(relationshipToString(rel));
assert(JSON.stringify(readBack.sort()) === JSON.stringify([...tuples].sort()), `tuples round-trip through SpiceDB: ${readBack}`);
console.log("tuples round-trip:", readBack.join("  "));

// --- Paged reads and lookups across several pages ---
const paged = new Transaction();
for (let i = 0; i < 7; i++)
    paged.touch(relationship(`document:paged-${i}`, "viewer", "user:pager"));
const { revision } = await client.write(paged);
for (const consistency of [full(), atLeast(revision)]) {
    const items = [];
    for await (const item of client.readRelationships({ filter: { resourceType: "document", subject: { subjectType: "user", subjectId: "pager" } }, consistency, pageSize: 2, limit: 5 }))
        items.push(item);
    assert(items.length === 5, `read limit 5 across pages of 2 (${consistency}) got ${items.length}`);
    assert(items.every(i => i.readAt.toString() !== "" && i.afterResultCursor), "read items carry read-at and cursor");
    // Resume after the third item: repeat the request and page size.
    const resumed = [];
    for await (const item of client.readRelationships({ filter: { resourceType: "document", subject: { subjectType: "user", subjectId: "pager" } }, consistency, pageSize: 2, cursor: items[2].afterResultCursor }))
        resumed.push(item.resource.objectId);
    assert(resumed.length === 4 && !resumed.includes(items[2].resource.objectId), `resume from a read cursor: ${resumed}`);

    const ids: string[] = [];
    for await (const r of client.lookupResources({ resourceType: "document", permission: "view", subject: { object: { objectType: "user", objectId: "pager" } }, consistency, pageSize: 2, limit: 5 }))
        ids.push(r.resourceId);
    assert(ids.length === 5, `lookup limit 5 across pages of 2 (${consistency}) got ${ids.length}`);
}
console.log("paging: reads and lookups crossed pages with full and at-least consistency");

// --- Import: batch size and the server's status on a duplicate ---
const imported = Array.from({ length: 25 }, (_, i) => relationship(`document:import-${i}`, "viewer", "user:importer"));
assert(await client.importRelationships(imported, { batchSize: 10 }) === 25n, "import in batches of 10");
const duplicate = await client.importRelationships([relationship("document:import-new", "viewer", "user:importer"), imported[0]], { batchSize: 1 }).catch(e => e);
assert(duplicate instanceof AlreadyExistsError, `duplicate import reports ALREADY_EXISTS, got ${duplicate}`);
assert(duplicate.code === Code.AlreadyExists && errorCode(duplicate) === Code.AlreadyExists && toConnectError(duplicate).code === Code.AlreadyExists, "error code interop");
assert(typeof duplicate.serverMessage === "string" && !duplicate.serverMessage.startsWith("["), "server message is verbatim");
// One import call is one transaction: the aborted import wrote nothing.
for await (const _ of client.readRelationships({ filter: { resourceType: "document", resourceId: "import-new" }, consistency: full() }))
    assert(false, "an aborted import must not commit any of its relationships");
console.log("import: duplicate reported as", duplicate.name, "-", duplicate.serverMessage);

// --- Bulk checks with per-item errors ---
const check = (resourceType: string) => ({ resource: { objectType: resourceType, objectId: "paged-0" }, permission: "view", subject: { object: { objectType: "user", objectId: "pager" } }, consistency: full() });
const bulk = await client.checkBulk([check("document"), check("nosuchtype"), check("document")], { itemErrors: BulkItemErrors.Returned });
assert(bulk[0].hasPermission() && bulk[2].hasPermission(), "other items still decided");
assert(bulk[1].error !== undefined && !bulk[1].hasPermission() && bulk[1].error.inputIndex === 1, "errored item carries its error and never grants");
console.log("checkBulk: item 1 failed with", bulk[1].error?.name, "while items 0 and 2 granted");
assert(protoconv.checkRequestToProto(check("document")).resource?.objectId === "paged-0", "protoconv builds the request the client sends");

// --- All-or-nothing delete and delete-all progress ---
const tooMany = await client.deleteRelationships({ resourceType: "document", subject: { subjectType: "user", subjectId: "importer" } }, { partial: DeletePartial.Disallowed, limit: 5 }).catch(e => e);
assert(tooMany instanceof InvalidArgumentError, `all-or-nothing delete over its limit is rejected, got ${tooMany}`);
const progress: DeleteResult[] = [];
const done = await client.deleteAllRelationshipsNonAtomic({ resourceType: "document", subject: { subjectType: "user", subjectId: "importer" } }, { pageSize: 10, onProgress: p => progress.push(p) });
assert(done.deletedCount === 25n && progress.length >= 3 && progress.at(-1)!.deletedCount === 25n, `delete-all progress ${progress.map(p => p.deletedCount)}`);
const exact = await client.deleteRelationships({ resourceType: "document", resourceId: "paged-0" }, { partial: DeletePartial.Disallowed });
assert(exact.deletedCount === 1n && exact.progress === "complete", "all-or-nothing delete with the server's default limit");
console.log("delete: progress", progress.map(p => p.deletedCount).join(" -> "));

// Leave no caveated or expiring relationships behind: later examples share
// this SpiceDB and write schemas without the caveat.
await client.deleteAllRelationshipsNonAtomic({ resourceType: "document" });
client.close();
console.log("tooling_escape_hatches: OK");
