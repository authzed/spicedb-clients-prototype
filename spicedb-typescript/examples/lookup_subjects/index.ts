/**
 * Example: Subject lookup
 *
 * Demonstrates looking up all subjects that have access to a resource.
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
txn.touch(relationship("document:readme", "editor", "user:sally"));
await client.write(txn);

// Find all users who can view a document
console.log("Users who can view document:readme:");
const found = new Set<string>();
for await (const subjectId of client.lookupSubjects(
  {
    resourceType: "document",
    resourceId: "readme",
    permission: "view",
    subjectType: "user",
  },
  full(),
)) {
  console.log(`  user:${subjectId}`);
  found.add(subjectId);
}

assert(found.has("jimmy"), "expected jimmy in results");
assert(found.has("sally"), "expected sally in results (editor implies view)");

console.log("lookup_subjects: PASS");
