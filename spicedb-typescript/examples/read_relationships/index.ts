import { TransportSecurity } from "../../src/index.js";
/**
 * Example: Reading relationships with async iterator
 *
 * Demonstrates reading relationships using filters and streaming.
 */
import { createSpiceDBClient, Transaction, relationship, full, type Relationship, } from "../../src/index.js";
function assert(condition: boolean, message: string): void {
    if (!condition) {
        console.error(`ASSERTION FAILED: ${message}`);
        process.exit(1);
    }
}
// Endpoint and token come from the environment so the example runs against
// whichever SpiceDB the caller started; the defaults match
// docker-compose.test.yml.
const endpoint = process.env.SPICEDB_ENDPOINT || "localhost:50051";
const token = process.env.SPICEDB_TOKEN || "testtoken";
const client = createSpiceDBClient(endpoint, token, {
    transportSecurity: TransportSecurity.Plaintext
});
// Setup: write schema and data
await client.writeSchema(`
definition user {}

definition document {
  relation viewer: user
  relation editor: user
  relation owner: user
  permission view = viewer + editor + owner
  permission edit = editor + owner
  permission delete = owner
}
`);
const txn = new Transaction();
txn.touch(relationship("document:readme", "viewer", "user:jimmy"));
txn.touch(relationship("document:readme", "editor", "user:sally"));
await client.write(txn);
// Read all relationships for a specific resource
console.log("All relationships for document:readme:");
const allRels: Relationship[] = [];
for await (const rel of client.readRelationships({
    filter: { resourceType: "document", resourceId: "readme" },
    consistency: full()
})) {
    console.log(`  ${rel.resource.objectType}:${rel.resource.objectId}#${rel.relation} -> ${rel.subject.object.objectType}:${rel.subject.object.objectId}`);
    allRels.push(rel);
}
assert(allRels.length >= 2, `expected at least 2 relationships, got ${allRels.length}`);
console.log(`Total document:readme relationships: ${allRels.length}`);
// Read filtered by relation
console.log("\nAll document viewers:");
let viewerCount = 0;
for await (const rel of client.readRelationships({
    filter: { resourceType: "document", relation: "viewer" },
    consistency: full()
})) {
    console.log(`  ${rel.resource.objectId} -> ${rel.subject.object.objectType}:${rel.subject.object.objectId}`);
    viewerCount++;
}
assert(viewerCount >= 1, "expected at least one viewer relationship");
// -----------------------------------------------------------------------
// Stopping a stream early: break/return automatically cancel the transport.
// A signal also interrupts a pending next() when cancellation comes from
// outside the consumer, such as a request being cancelled.
// -----------------------------------------------------------------------
console.log("\nStopping a stream early with an AbortSignal:");
const abortController = new AbortController();
let firstRel: Relationship | undefined;
for await (const rel of client.readRelationships({
    filter: { resourceType: "document", resourceId: "readme" },
    consistency: full()
}, { signal: abortController.signal })) {
    firstRel = rel;
    console.log(`  took ${rel.resource.objectType}:${rel.resource.objectId}#${rel.relation}, then aborting`);
    abortController.abort();
    break;
}
assert(firstRel !== undefined, "expected to read at least one relationship before aborting");
// Clean up so later examples that write a narrower schema aren't blocked by
// leftover relationships (examples run in sequence against one shared
// SpiceDB instance).
await client.deleteRelationships({ resourceType: "document" }, {
    limit: 1000
});
// Release the underlying transport now that this example is done with it.
client.close();
console.log("read_relationships: PASS");
