/**
 * Example: Resource lookup
 *
 * Demonstrates looking up all resources a subject has access to, including
 * how to read the `permissionship` of each result. A permissionship of
 * `"conditionalPermission"` means the match depends on caveat context that
 * wasn't supplied — callers must not treat it as an unconditional grant.
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

// Endpoint and token come from the environment so the example runs against
// whichever SpiceDB the caller started; the defaults match
// docker-compose.test.yml.
const endpoint = process.env.SPICEDB_ENDPOINT || "localhost:50051";
const token = process.env.SPICEDB_TOKEN || "testtoken";

const client = createSpiceDBClient(endpoint, token, {
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
for await (const resource of client.lookupResources(
  {
    resourceType: "document",
    permission: "view",
    subjectType: "user",
    subjectId: "jimmy",
  },
  full(),
)) {
  console.log(
    `  document:${resource.resourceId} (permissionship=${resource.permissionship})`,
  );
  if (resource.permissionship !== "hasPermission") {
    // A conditional result means caveat context is missing; partialCaveat
    // lists which context. Never treat a conditional result as a full
    // grant.
    console.error(
      `ASSERTION FAILED: unexpected permissionship for document:${resource.resourceId}: ` +
        `${resource.permissionship} (missing context: ${JSON.stringify(resource.partialCaveat)})`,
    );
    process.exit(1);
  }
  // lookedUpAt is the revision this result was computed at — thread it into
  // atLeast()/atLeastOrFull() for read-your-writes on a later call.
  assert(
    resource.lookedUpAt !== "",
    `expected a non-empty lookedUpAt token for document:${resource.resourceId}`,
  );
  found.add(resource.resourceId);
}

assert(found.has("readme"), "expected readme in results");
assert(found.has("design"), "expected design in results (editor implies view)");

// Clean up so later examples that write a narrower schema aren't blocked by
// leftover relationships (examples run in sequence against one shared
// SpiceDB instance).
await client.deleteRelationships({ resourceType: "document" });

// Release the underlying transport now that this example is done with it.
client.close();

console.log("lookup_resources: PASS");
