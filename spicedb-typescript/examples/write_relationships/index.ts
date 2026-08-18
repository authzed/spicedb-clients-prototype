/**
 * Example: Writing relationships with the transaction builder
 *
 * Demonstrates create, touch, delete operations and preconditions.
 */
import {
  createSpiceDBClient,
  Transaction,
  relationship,
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

// Setup: write schema
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

// Build a transaction with multiple operations
const txn = new Transaction();
txn.touch(relationship("document:readme", "viewer", "user:jimmy"));
txn.touch(relationship("document:readme", "editor", "user:sally"));

// Add a precondition: fail if any matching relationships exist
txn.mustNotMatch({
  resourceType: "document",
  resourceId: "readme",
  resourceRelation: "owner",
  subjectType: "user",
  subjectId: "jimmy",
});

const revision = await client.write(txn);
console.log(`Written at revision: ${revision}`);
assert(revision !== "", "expected non-empty revision");

// Clean up so later examples that write a narrower schema aren't blocked by
// leftover relationships (examples run in sequence against one shared
// SpiceDB instance).
await client.deleteRelationships({ resourceType: "document" });

// Release the underlying transport now that this example is done with it.
client.close();

console.log("write_relationships: PASS");
