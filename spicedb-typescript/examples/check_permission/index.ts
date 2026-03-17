/**
 * Example: Basic permission check
 *
 * Demonstrates using checkPermission, checkPermissions, checkAny, and checkAll.
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
txn.touch(relationship("document:readme", "editor", "user:sally"));
const setupRevision = await client.write(txn);

// Single permission check
const check: CheckRequest = {
  resourceType: "document",
  resourceId: "readme",
  permission: "view",
  subjectType: "user",
  subjectId: "jimmy",
};

const allowed = await client.checkPermission(full(), check);
console.log(`user:jimmy can view document:readme: ${allowed}`);
assert(allowed, "expected jimmy to have view permission");

// Bulk check: multiple permissions at once
const checks: CheckRequest[] = [
  { ...check, permission: "view" },
  { ...check, permission: "edit" },
];

const results = await client.checkPermissions(atLeast(setupRevision), ...checks);
console.log("Bulk check results:", results);
assert(results[0] === true, "expected jimmy can view");
assert(results[1] === false, "expected jimmy cannot edit");

// checkAny: does jimmy have any of these permissions?
const hasAny = await client.checkAny(full(), ...checks);
console.log(`user:jimmy has any permission: ${hasAny}`);
assert(hasAny, "expected jimmy to have at least one permission");

// checkAll: does jimmy have all permissions?
const hasAll = await client.checkAll(full(), ...checks);
console.log(`user:jimmy has all permissions: ${hasAll}`);
assert(!hasAll, "expected jimmy to NOT have all permissions");

console.log("check_permission: PASS");
