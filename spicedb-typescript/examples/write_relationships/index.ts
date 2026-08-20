/**
 * Example: Writing relationships with the transaction builder
 *
 * Demonstrates create, touch, delete operations and preconditions.
 */
import {
  createSpiceDBClient,
  FailedPreconditionError,
  Transaction,
  relationship,
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

// A failed precondition arrives with SpiceDB's structured explanation
// attached, not just a message: the typed error carries the
// authzed.api.v1.ErrorReason name and the metadata naming which precondition
// did not hold, so recovery can be written against data rather than against a
// parsed string.
const doomed = new Transaction();
doomed.touch(relationship("document:seconddoc", "viewer", "user:jimmy"));
doomed.mustMatch({
  resourceType: "document",
  resourceId: "readme",
  resourceRelation: "owner",
  subjectType: "user",
  subjectId: "nobody",
});

try {
  await client.write(doomed);
  assert(false, "expected the unsatisfiable precondition to fail the write");
} catch (err) {
  assert(
    err instanceof FailedPreconditionError,
    `expected FailedPreconditionError, got ${String(err)}`,
  );
  const spiceErr = err as FailedPreconditionError;
  console.log(
    `Precondition failed: reason=${spiceErr.reason} domain=${spiceErr.reasonDomain}`,
    spiceErr.reasonMetadata,
  );
  assert(
    spiceErr.reason === "ERROR_REASON_WRITE_OR_DELETE_PRECONDITION_FAILURE",
    `expected the write/delete precondition reason, got ${spiceErr.reason}`,
  );
  assert(
    Object.keys(spiceErr.reasonMetadata).length > 0,
    "expected the reason metadata to name the failing precondition",
  );
}

// Clean up so later examples that write a narrower schema aren't blocked by
// leftover relationships (examples run in sequence against one shared
// SpiceDB instance).
await client.deleteRelationships({ resourceType: "document" });

// Release the underlying transport now that this example is done with it.
client.close();

console.log("write_relationships: PASS");
