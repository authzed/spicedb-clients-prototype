/**
 * Example: Bulk checks and exports
 *
 * Demonstrates bulk permission checks and bulk relationship exports.
 */
import {
  createSpiceDBClient,
  Transaction,
  relationship,
  full,
  atLeast,
  type CheckRequest,
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
txn.touch(relationship("document:design", "editor", "user:sally"));
const setupRevision = await client.write(txn);

// Bulk check permissions
const checks: CheckRequest[] = [
  {
    resourceType: "document",
    resourceId: "readme",
    permission: "view",
    subjectType: "user",
    subjectId: "jimmy",
  },
  {
    resourceType: "document",
    resourceId: "design",
    permission: "edit",
    subjectType: "user",
    subjectId: "sally",
  },
];

const results = await client.checkPermissions(atLeast(setupRevision), ...checks);
for (let i = 0; i < checks.length; i++) {
  const c = checks[i];
  console.log(
    `${c.subjectType}:${c.subjectId} ${c.permission} ${c.resourceType}:${c.resourceId}: ${results[i]}`,
  );
  assert(results[i] === true, `expected check ${i} to be true`);
}

// Export all document relationships
console.log("\nExporting all document relationships:");
let exportCount = 0;
for await (const rel of client.exportBulkRelationships(full(), {
  resourceType: "document",
})) {
  console.log(
    `  ${rel.resourceType}:${rel.resourceId}#${rel.resourceRelation} -> ${rel.subjectType}:${rel.subjectId}`,
  );
  exportCount++;
}
assert(exportCount >= 2, `expected at least 2 exported relationships, got ${exportCount}`);

// checkAny / checkAll
const anyAllowed = await client.checkAny(atLeast(setupRevision), ...checks);
console.log(`\nAny permission granted: ${anyAllowed}`);
assert(anyAllowed, "expected at least one permission granted");

const allAllowed = await client.checkAll(atLeast(setupRevision), ...checks);
console.log(`All permissions granted: ${allAllowed}`);
assert(allAllowed, "expected all permissions granted");

console.log("bulk_operations: PASS");
