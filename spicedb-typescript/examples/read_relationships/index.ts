/**
 * Example: Reading relationships with async iterator
 *
 * Demonstrates reading relationships using filters and streaming.
 */
import {
  createSpiceDBClient,
  Transaction,
  relationship,
  full,
  type Relationship,
} from "../../src/index.js";

function assert(condition: boolean, message: string): void {
  if (!condition) {
    console.error(`ASSERTION FAILED: ${message}`);
    process.exit(1);
  }
}

const client = createSpiceDBClient("localhost:50051", "testtoken", {
  insecure: true,
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
for await (const rel of client.readRelationships(
  { resourceType: "document", resourceId: "readme" },
  full(),
)) {
  console.log(
    `  ${rel.resourceType}:${rel.resourceId}#${rel.resourceRelation} -> ${rel.subjectType}:${rel.subjectId}`,
  );
  allRels.push(rel);
}

assert(allRels.length >= 2, `expected at least 2 relationships, got ${allRels.length}`);
console.log(`Total document:readme relationships: ${allRels.length}`);

// Read filtered by relation
console.log("\nAll document viewers:");
let viewerCount = 0;
for await (const rel of client.readRelationships(
  { resourceType: "document", resourceRelation: "viewer" },
  full(),
)) {
  console.log(`  ${rel.resourceId} -> ${rel.subjectType}:${rel.subjectId}`);
  viewerCount++;
}
assert(viewerCount >= 1, "expected at least one viewer relationship");

// Clean up so later examples that write a narrower schema aren't blocked by
// leftover relationships (examples run in sequence against one shared
// SpiceDB instance).
await client.deleteRelationships({ resourceType: "document" });

console.log("read_relationships: PASS");
