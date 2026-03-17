/**
 * Example: Resource lookup
 *
 * Demonstrates looking up all resources a subject has access to.
 */
import {
  createSpiceDBClient,
  Transaction,
  relationship,
  full,
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
txn.touch(relationship("document:design", "editor", "user:jimmy"));
await client.write(txn);

// Find all documents that user:jimmy can view
console.log("Documents user:jimmy can view:");
const found = new Set<string>();
for await (const resourceId of client.lookupResources(
  {
    resourceType: "document",
    permission: "view",
    subjectType: "user",
    subjectId: "jimmy",
  },
  full(),
)) {
  console.log(`  document:${resourceId}`);
  found.add(resourceId);
}

assert(found.has("readme"), "expected readme in results");
assert(found.has("design"), "expected design in results (editor implies view)");

console.log("lookup_resources: PASS");
