/**
 * Example: Basic permission check
 *
 * Demonstrates using checkPermission, checkPermissions, checkAny, and
 * checkAll — including the CheckResult returned for a caveated relationship
 * whose context was not supplied at check time.
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

// Setup: write schema and data. The "active" caveat and conditional_viewer
// relation exist to demonstrate a conditionalPermission check result below.
await client.writeSchema(`
definition user {}

caveat active(now int) {
  now < 100
}

definition document {
  relation viewer: user
  relation editor: user
  relation owner: user
  relation conditional_viewer: user with active
  permission view = viewer + editor + owner
  permission edit = editor + owner
  permission delete = owner
  permission conditional_view = conditional_viewer
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

const result = await client.checkPermission(full(), check);
console.log(
  `user:jimmy can view document:readme: ${result.hasPermission()} (permissionship: ${result.permissionship})`,
);
assert(result.hasPermission(), "expected jimmy to have view permission");

// checkedAt is the revision this check was evaluated at. Thread it into
// atLeast()/atLeastOrFull() to make a later read observe this check (and
// everything it observed) — read-your-writes for checks.
assert(result.checkedAt !== "", "expected a non-empty checkedAt token");

// Bulk check: multiple permissions at once
const checks: CheckRequest[] = [
  { ...check, permission: "view" },
  { ...check, permission: "edit" },
];

const results = await client.checkPermissions(atLeast(setupRevision), ...checks);
console.log(
  "Bulk check results:",
  results.map((r) => r.hasPermission()),
);
assert(results[0].hasPermission(), "expected jimmy can view");
assert(!results[1].hasPermission(), "expected jimmy cannot edit");

// checkAny: does jimmy have any of these permissions? A conditionalPermission
// result would NOT count here — only an unconditional grant does.
const hasAny = await client.checkAny(full(), ...checks);
console.log(`user:jimmy has any permission: ${hasAny}`);
assert(hasAny, "expected jimmy to have at least one permission");

// checkAll: does jimmy have all permissions?
const hasAll = await client.checkAll(full(), ...checks);
console.log(`user:jimmy has all permissions: ${hasAll}`);
assert(!hasAll, "expected jimmy to NOT have all permissions");

// -----------------------------------------------------------------------
// Conditional (caveated) check: write a caveated relationship without
// supplying its context, then check it with no context either. The server
// cannot evaluate "now < 100" without "now", so it reports
// conditionalPermission rather than guessing — and hasPermission() MUST be
// false for a conditional result, or callers would be granting access on an
// unevaluated condition. This is the regression guard for the fail-open
// this client used to have (conditional used to collapse into `true`).
// -----------------------------------------------------------------------
const caveatedTxn = new Transaction();
caveatedTxn.touch({
  resourceType: "document",
  resourceId: "conditionaldoc",
  resourceRelation: "conditional_viewer",
  subjectType: "user",
  subjectId: "jimmy",
  caveatName: "active",
});
await client.write(caveatedTxn);

const condResult = await client.checkPermission(full(), {
  resourceType: "document",
  resourceId: "conditionaldoc",
  permission: "conditional_view",
  subjectType: "user",
  subjectId: "jimmy",
});
console.log(
  `user:jimmy can conditionally view document:conditionaldoc: ${condResult.hasPermission()} ` +
    `(permissionship: ${condResult.permissionship}, missing context: ${condResult.missingContext})`,
);
assert(
  condResult.permissionship === "conditionalPermission",
  `expected conditionalPermission with no caveat context supplied, got ${condResult.permissionship}`,
);
assert(
  !condResult.hasPermission(),
  "a conditionalPermission result must never report hasPermission() === true",
);
assert(
  condResult.missingContext.length === 1 && condResult.missingContext[0] === "now",
  `expected missingContext == ["now"], got ${condResult.missingContext}`,
);

// Clean up so later examples that write a narrower schema aren't blocked by
// leftover relationships (examples run in sequence against one shared
// SpiceDB instance).
await client.deleteRelationships({ resourceType: "document" });

console.log("check_permission: PASS");
