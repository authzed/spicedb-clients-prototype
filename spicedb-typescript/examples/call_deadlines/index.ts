/**
 * Example: Call deadlines
 *
 * Demonstrates the client-level `defaultTimeoutMs` option on the documented
 * `createSpiceDBClient` factory, a per-call `timeoutMs` override, and that
 * bulk import (`importBulkRelationships`) is a client-streaming call that is
 * NOT bounded by `defaultTimeoutMs` -- see root DESIGN.md, "RULE: A unary
 * call must have a deadline".
 */
import {
  createSpiceDBClient,
  Transaction,
  relationship,
  full,
  type Relationship,
  type CheckRequest,
} from "../../src/index.js";

function assert(condition: boolean, message: string): void {
  if (!condition) {
    console.error(`ASSERTION FAILED: ${message}`);
    process.exit(1);
  }
}

// defaultTimeoutMs applies to every unary call that doesn't pass its own
// timeoutMs. This is the documented, real construction path -- not a mock --
// so a signature drift here (e.g. the option silently disappearing from
// createSpiceDBClient's options type) would fail this example to typecheck,
// not just a unit test against a stalling stub.
// Endpoint and token come from the environment so the example runs against
// whichever SpiceDB the caller started; the defaults match
// docker-compose.test.yml.
const endpoint = process.env.SPICEDB_ENDPOINT || "localhost:50051";
const token = process.env.SPICEDB_TOKEN || "testtoken";

const client = createSpiceDBClient(endpoint, token, {
  insecure: true,
  defaultTimeoutMs: 5000,
});

await client.writeSchema(`
definition user {}

definition document {
  relation viewer: user
  permission view = viewer
}
`);

const txn = new Transaction();
txn.touch(relationship("document:readme", "viewer", "user:alice"));
await client.write(txn);

// Bound by the 5s default set above.
const check: CheckRequest = {
  resourceType: "document",
  resourceId: "readme",
  permission: "view",
  subjectType: "user",
  subjectId: "alice",
};
const result = await client.checkPermission(full(), check);
console.log(
  `user:alice can view document:readme (default timeout): ${result.hasPermission()}`,
);
assert(result.hasPermission(), "expected alice to have view permission");

// A per-call timeoutMs overrides the client default for this one call. 2s
// is generous for a real call against a local SpiceDB -- this exercises the
// real timeoutMs option end-to-end, not testing how small a timeout can be.
const overriddenResult = await client.checkPermission(full(), check, {
  timeoutMs: 2000,
});
console.log(
  `user:alice can view document:readme (2s per-call timeout): ${overriddenResult.hasPermission()}`,
);
assert(
  overriddenResult.hasPermission(),
  "expected alice to have view permission",
);

// importBulkRelationships (client-streaming) is explicitly excluded from
// defaultTimeoutMs: its duration scales with the size of the caller's
// dataset, not with server latency. Calling it with no timeoutMs at all --
// as below -- must still succeed; if a future change accidentally routed the
// unary default into this call, a large enough import would start failing
// with DeadlineExceededError well before it finished.
const importRels: Relationship[] = Array.from({ length: 50 }, (_, i) => ({
  resourceType: "document",
  resourceId: `bulk-${i}`,
  resourceRelation: "viewer",
  subjectType: "user",
  subjectId: "alice",
}));
const numLoaded = await client.importBulkRelationships(importRels);
console.log(`imported ${numLoaded} relationships with no timeout bound`);
assert(numLoaded === 50n, "expected 50 relationships imported");

// A caller-supplied timeoutMs on the same client-streaming call must still
// be honored -- the exclusion is from the *default*, not from the ability to
// bound the call at all.
const moreImportRels: Relationship[] = Array.from(
  { length: 50 },
  (_, i) => ({
    resourceType: "document",
    resourceId: `bulk2-${i}`,
    resourceRelation: "viewer",
    subjectType: "user",
    subjectId: "alice",
  }),
);
const numLoadedBounded = await client.importBulkRelationships(
  moreImportRels,
  { timeoutMs: 30000 },
);
console.log(
  `imported ${numLoadedBounded} relationships with an explicit 30s timeout`,
);
assert(numLoadedBounded === 50n, "expected 50 relationships imported");

// Clean up so later examples that write a narrower schema aren't blocked by
// leftover relationships (examples run in sequence against one shared
// SpiceDB instance).
await client.deleteRelationships({ resourceType: "document" });

// Release the underlying transport now that this example is done with it.
client.close();

console.log("call_deadlines: PASS");
