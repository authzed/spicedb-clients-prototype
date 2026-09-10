/**
 * Example: deleteRelationships({ limit, autoPage })
 *
 * By default `deleteRelationships` does not auto-page: a `limit`-bounded
 * call deletes at most `limit` matches and leaves the rest for a caller to
 * clean up with another call. `autoPage: true` closes that gap -- it loops
 * internally, threading the cursor a partial response carries into the
 * next request, until every match is gone.
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

await client.writeSchema(`
definition user {}

definition document {
  relation viewer: user
}
`);

const totalRels = 50;
const pageSize = 20;
const txn = new Transaction();
for (let i = 0; i < totalRels; i++) {
  txn.touch(relationship(`document:doc${i}`, "viewer", "user:alice"));
}
await client.write(txn);

async function countRemaining(): Promise<number> {
  let count = 0;
  for await (const _ of client.readRelationships(
    { resourceType: "document" },
    full(),
  )) {
    count++;
  }
  return count;
}

// Default behavior is unchanged: a limited call deletes at most `limit` and
// stops there, leaving the rest for a caller to clean up manually.
const boundedRevision = await client.deleteRelationships(
  { resourceType: "document" },
  { limit: pageSize },
);
assert(boundedRevision !== "", "expected a non-empty revision");
const afterBounded = await countRemaining();
console.log(
  `Bounded delete (limit=${pageSize}, no autoPage): ${totalRels - afterBounded} deleted, ${afterBounded} remaining`,
);
assert(
  afterBounded === totalRels - pageSize,
  `expected exactly ${pageSize} deleted and ${totalRels - pageSize} left, but ${afterBounded} remain`,
);

// autoPage: true loops internally -- using `limit` as the per-page size --
// until every match is gone, threading each response's cursor into the
// next request when the datastore supports cursored deletion. Whether this
// datastore actually populates a cursor is an implementation detail this
// example does not depend on: the loop works either way, since a request
// with no cursor still finds the remaining matches by filter alone.
const finalRevision = await client.deleteRelationships(
  { resourceType: "document" },
  { limit: pageSize, autoPage: true },
);
assert(finalRevision !== "", "expected a non-empty revision");
const afterAutoPage = await countRemaining();
console.log(
  `autoPage delete: ${afterBounded - afterAutoPage} deleted, ${afterAutoPage} remaining`,
);
assert(
  afterAutoPage === 0,
  `expected every remaining relationship deleted, but ${afterAutoPage} are left`,
);

// Release the underlying transport now that this example is done with it.
client.close();

console.log("delete_relationships_autopage: PASS");
