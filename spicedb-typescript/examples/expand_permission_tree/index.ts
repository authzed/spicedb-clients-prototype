/**
 * Example: Expand permission tree
 *
 * Demonstrates expandPermissionTree and walking the native PermissionTree it
 * returns -- a tree of `expandedObject`/`expandedRelation` nodes where each
 * node is either an `intermediate` node (children combined by a
 * TreeOperation) or a `leaf` node (concrete SubjectRefs). No `unknown` or
 * proto types are involved.
 */
import {
  createSpiceDBClient,
  Transaction,
  relationship,
  full,
  type PermissionTree,
  type SubjectRef,
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
}
`);

const txn = new Transaction();
txn.touch(relationship("document:readme", "viewer", "user:jimmy"));
txn.touch(relationship("document:readme", "editor", "user:sally"));
txn.touch(relationship("document:readme", "owner", "user:alice"));
await client.write(txn);

// Expand the "view" permission tree for document:readme
const { expandedAt, treeRoot } = await client.expandPermissionTree(full(), {
  resourceType: "document",
  resourceId: "readme",
  permission: "view",
});
console.log(`Expanded at revision: ${expandedAt}`);

// Recursively walk the native PermissionTree, printing each node's
// expandedObject/expandedRelation, the operation at intermediate nodes, and
// the concrete subjects at leaf nodes. Returns every leaf subject found.
function walk(node: PermissionTree, depth = 0): SubjectRef[] {
  const indent = "  ".repeat(depth);
  console.log(
    `${indent}${node.expandedObject.objectType}:${node.expandedObject.objectId}#${node.expandedRelation}`,
  );

  if (node.intermediate) {
    console.log(`${indent}  operation: ${node.intermediate.operation}`);
    return node.intermediate.children.flatMap((child) => walk(child, depth + 1));
  }

  if (node.leaf) {
    return node.leaf.subjects.map((subject) => {
      console.log(`${indent}  leaf subject: ${subject.subjectType}:${subject.subjectId}`);
      return subject;
    });
  }

  return [];
}

const leafSubjects = walk(treeRoot);
const subjectIds = new Set(leafSubjects.map((s) => `${s.subjectType}:${s.subjectId}`));

console.log("\nExpanded subjects:", [...subjectIds].sort());
assert(subjectIds.has("user:jimmy"), "expected jimmy in expanded tree");
assert(subjectIds.has("user:sally"), "expected sally in expanded tree");
assert(subjectIds.has("user:alice"), "expected alice in expanded tree");
assert(
  subjectIds.size === 3,
  `expected exactly 3 expanded subjects, got ${subjectIds.size}`,
);

// Clean up so later examples that write a narrower schema aren't blocked by
// leftover relationships (examples run in sequence against one shared
// SpiceDB instance).
await client.deleteRelationships({ resourceType: "document" });

console.log("\nexpand_permission_tree: PASS");
