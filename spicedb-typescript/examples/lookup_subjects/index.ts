/**
 * Example: Subject lookup
 *
 * Demonstrates looking up all subjects that have access to a resource,
 * including the wildcard/excluded-subjects case: when the server resolves a
 * wildcard "*" subject, `excludedSubjects` lists the subjects carved out of
 * that wildcard grant. Treating a wildcard match as "everyone has access"
 * without checking `excludedSubjects` is a real over-grant risk.
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

// Setup: write schema and data. `banned` carves subjects out of the
// public/wildcard viewer grant below.
await client.writeSchema(`
definition user {}

definition document {
  relation viewer: user | user:*
  relation editor: user
  relation owner: user
  relation banned: user
  permission view = (viewer + editor + owner) - banned
  permission edit = editor + owner
  permission delete = owner
}
`);

const txn = new Transaction();
txn.touch(relationship("document:readme", "editor", "user:bob"));
// Grant view to every user (wildcard), except those in `banned`.
txn.touch(relationship("document:readme", "viewer", "user:*"));
txn.touch(relationship("document:readme", "banned", "user:eve"));
await client.write(txn);

// Find all users who can view a document
console.log("Users who can view document:readme:");
let sawWildcard = false;
const excludedIds = new Set<string>();
for await (const result of client.lookupSubjects(
  {
    resourceType: "document",
    resourceId: "readme",
    permission: "view",
    subjectType: "user",
  },
  full(),
)) {
  console.log(
    `  user:${result.subject.subjectId} (permissionship=${result.subject.permissionship})`,
  );

  if (result.subject.subjectId === "*") {
    sawWildcard = true;
    // This is the over-grant-risk case: "*" alone would mean "every user",
    // but excludedSubjects carves specific subjects back out. Never grant
    // access based on the wildcard match alone.
    for (const excluded of result.excludedSubjects) {
      console.log(`    excluded from wildcard: user:${excluded.subjectId}`);
      excludedIds.add(excluded.subjectId);
    }
  }
}

assert(sawWildcard, "expected a wildcard (*) subject in results");
assert(excludedIds.has("eve"), "expected eve to be excluded from the wildcard grant");

// bob has view via `editor`, independent of the wildcard/banned rule.
const found = new Set<string>();
for await (const result of client.lookupSubjects(
  {
    resourceType: "document",
    resourceId: "readme",
    permission: "edit",
    subjectType: "user",
  },
  full(),
)) {
  found.add(result.subject.subjectId);
}
assert(found.has("bob"), "expected bob in edit results");

console.log("lookup_subjects: PASS");
