/**
 * Example: Bulk checks, bulk import, and bulk export
 *
 * Demonstrates bulk permission checks, bulk relationship import via
 * importBulkRelationships, and bulk relationship export.
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
    `${c.subjectType}:${c.subjectId} ${c.permission} ${c.resourceType}:${c.resourceId}: ` +
      `${results[i].hasPermission()} (permissionship: ${results[i].permissionship})`,
  );
  assert(results[i].hasPermission(), `expected check ${i} to have permission`);
}

// Bulk import: load many relationships in a single streaming call
const bulkRels = [
  relationship("document:readme", "owner", "user:admin"),
  relationship("document:design", "owner", "user:admin"),
  relationship("document:notes", "viewer", "user:jimmy"),
];
const numLoaded = await client.importBulkRelationships(bulkRels);
console.log(`\nBulk-imported ${numLoaded} relationships`);
assert(
  numLoaded === BigInt(bulkRels.length),
  `expected ${bulkRels.length} relationships imported, got ${numLoaded}`,
);

// The array above is fine when the data is already in memory. For a dataset
// bigger than memory -- which is what a bulk import is for -- hand in a
// generator instead: relationships are converted and batched as they are
// pulled, so only one batch is ever resident. An async generator works the
// same way, which is what reading from a DB cursor or a file stream looks
// like in practice.
function* generatedRels(count: number) {
  for (let i = 0; i < count; i++) {
    yield relationship(`document:generated${i}`, "viewer", "user:sally");
  }
}

const numGenerated = await client.importBulkRelationships(generatedRels(5));
console.log(`Bulk-imported ${numGenerated} relationships from a generator`);
assert(
  numGenerated === 5n,
  `expected 5 relationships imported from the generator, got ${numGenerated}`,
);

// Export all document relationships (includes both the initial write and
// the bulk import above)
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
const expectedExportCount = 2 + bulkRels.length;
assert(
  exportCount >= expectedExportCount,
  `expected at least ${expectedExportCount} exported relationships, got ${exportCount}`,
);

// checkAny / checkAll
const anyAllowed = await client.checkAny(atLeast(setupRevision), ...checks);
console.log(`\nAny permission granted: ${anyAllowed}`);
assert(anyAllowed, "expected at least one permission granted");

const allAllowed = await client.checkAll(atLeast(setupRevision), ...checks);
console.log(`All permissions granted: ${allAllowed}`);
assert(allAllowed, "expected all permissions granted");

// Clean up so later examples that write a narrower schema aren't blocked by
// leftover relationships (examples run in sequence against one shared
// SpiceDB instance).
await client.deleteRelationships({ resourceType: "document" });

// Release the underlying transport now that this example is done with it.
client.close();

console.log("bulk_operations: PASS");
