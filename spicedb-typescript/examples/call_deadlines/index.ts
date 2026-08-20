/**
 * Example: Call deadlines
 *
 * Demonstrates the client-level `defaultTimeoutMs` option on the documented
 * `createSpiceDBClient` factory, a per-call `timeoutMs` override, and that
 * bulk import (`importBulkRelationships`) is a client-streaming call that is
 * NOT bounded by `defaultTimeoutMs` -- see root DESIGN.md, "RULE: A unary
 * call must have a deadline".
 *
 * The failure that rule exists to close is a *wedged* server: one that accepts
 * the connection and then never answers. Nothing looks wrong at the transport
 * level, so an unbounded call hangs forever rather than erroring. Showing only
 * fast local calls succeeding would pass identically whether or not the
 * timeout ever reached the wire, so this example also stands up a socket that
 * behaves exactly that way and requires the call to come back
 * DeadlineExceededError on the caller's schedule.
 */
import { createServer, type Server } from "node:net";
import { once } from "node:events";
import {
  createSpiceDBClient,
  DeadlineExceededError,
  FailedPreconditionError,
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

// The schema below is narrower than the one most examples write, and they all
// share one SpiceDB. SpiceDB refuses a WriteSchema that drops a relation while
// a relationship still exists under it, so clear before writing, not after: an
// earlier example leaving `document:x#editor@user:y` behind is enough to fail
// this outright. Exactly one error is tolerated -- on a fresh server there is
// no `document` definition yet, which SpiceDB reports as FailedPrecondition.
try {
  await client.deleteRelationships({ resourceType: "document" });
} catch (err) {
  if (!(err instanceof FailedPreconditionError)) {
    throw err;
  }
}

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

// ---------------------------------------------------------------------------
// The case the rule is about: a server that never answers.
//
// This listener accepts TCP connections and never speaks HTTP/2, so the
// server preface never arrives. That is what a wedged SpiceDB looks like from
// a client -- an open, healthy-looking connection with no reply behind it --
// and it is why "the connection worked" is not a bound. Everything above this
// point passes whether or not `timeoutMs` reaches the wire; this does not.
// ---------------------------------------------------------------------------
const WEDGED_TIMEOUT_MS = 2_000;

const wedged: Server = createServer((socket) => {
  // Hold the connection open and send nothing, ever.
  socket.on("error", () => {});
});
wedged.listen(0, "127.0.0.1");
await once(wedged, "listening");
const wedgedAddress = wedged.address();
if (wedgedAddress === null || typeof wedgedAddress === "string") {
  console.error("ASSERTION FAILED: could not determine the wedged listener's port");
  process.exit(1);
}

const wedgedClient = createSpiceDBClient(
  `127.0.0.1:${wedgedAddress.port}`,
  token,
  { insecure: true, defaultTimeoutMs: WEDGED_TIMEOUT_MS },
);

// The call runs behind a watchdog. If the timeout never reaches the wire -- a
// client that accepted `defaultTimeoutMs` and never attached it, say -- this
// call does not return at all, and an example that simply awaited it would
// hang the CI job rather than fail it.
const watchdog = new Promise<never>((_resolve, reject) => {
  const timer = setTimeout(
    () =>
      reject(
        new Error(
          `a call with a ${WEDGED_TIMEOUT_MS}ms timeout had not returned after ` +
            `${WEDGED_TIMEOUT_MS + 15_000}ms against a server that never answers: ` +
            `the caller's timeout is not reaching the RPC`,
        ),
      ),
    WEDGED_TIMEOUT_MS + 15_000,
  );
  timer.unref?.();
});

const startedAt = Date.now();
const wedgedOutcome = await Promise.race([
  wedgedClient.checkPermission(full(), check).then(
    () => undefined,
    (err: unknown) => err,
  ),
  watchdog,
]);
const elapsedMs = Date.now() - startedAt;

console.log(`wedged server: ${String(wedgedOutcome)} after ${elapsedMs}ms`);

// The specific error matters. "An error occurred" would also be satisfied by
// an UnavailableError from a refused connection -- which is what this would
// degrade into if the listener above stopped accepting, and which says
// nothing at all about deadlines.
assert(
  wedgedOutcome instanceof DeadlineExceededError,
  `expected DeadlineExceededError from the wedged server, got: ${String(wedgedOutcome)}`,
);

// And it has to expire on the caller's schedule. The client retries transient
// failures, so the bound here allows for the retry budget rather than pinning
// a single attempt.
assert(
  elapsedMs < WEDGED_TIMEOUT_MS + 15_000,
  `the ${WEDGED_TIMEOUT_MS}ms timeout took ${elapsedMs}ms to fire: the caller's timeout is not bounding the call`,
);

// A per-call `timeoutMs` must bite the same way, overriding the client
// default rather than being quietly ignored on the per-call path.
const perCallOutcome = await Promise.race([
  wedgedClient
    .checkPermission(full(), check, { timeoutMs: 1_000 })
    .then(
      () => undefined,
      (err: unknown) => err,
    ),
  watchdog,
]);
assert(
  perCallOutcome instanceof DeadlineExceededError,
  `expected a per-call timeoutMs to expire against the wedged server, got: ${String(perCallOutcome)}`,
);

wedgedClient.close();
wedged.close();

// Clean up so later examples that write a narrower schema aren't blocked by
// leftover relationships (examples run in sequence against one shared
// SpiceDB instance).
await client.deleteRelationships({ resourceType: "document" });

// Release the underlying transport now that this example is done with it.
client.close();

console.log("call_deadlines: PASS");
