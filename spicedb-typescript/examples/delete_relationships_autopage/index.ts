import { TransportSecurity } from "../../src/index.js";
/**
 * Bounded deletion uses a required total limit and sends one RPC.
 * deleteAllRelationshipsNonAtomic uses a separate pageSize and returns cumulative progress.
 * The historical directory name is retained to match the integration runner.
 */
import { createSpiceDBClient, Transaction, relationship, full, } from "../../src/index.js";
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
    transportSecurity: TransportSecurity.Plaintext
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
    for await (const _ of client.readRelationships({
        filter: { resourceType: "document" },
        consistency: full()
    })) {
        count++;
    }
    return count;
}
// A bounded operation deletes at most limit matches in one RPC.
const boundedRevision = await client.deleteRelationships({ resourceType: "document" }, { limit: pageSize });
assert(boundedRevision.revision.toString() !== "", "expected a non-empty revision");
assert(boundedRevision.deletedCount === BigInt(pageSize), "expected bounded count");
assert(boundedRevision.progress === "partial", "expected remaining matches");
const afterBounded = await countRemaining();
console.log(`Bounded delete (limit=${pageSize}, single RPC): ${totalRels - afterBounded} deleted, ${afterBounded} remaining`);
assert(afterBounded === totalRels - pageSize, `expected exactly ${pageSize} deleted and ${totalRels - pageSize} left, but ${afterBounded} remain`);
// Delete-all shares one deadline across pages, rechecks preconditions per page,
// and resumes using the server cursor when supplied. Completion is not atomic.
const finalRevision = await client.deleteAllRelationshipsNonAtomic({ resourceType: "document" }, {
    pageSize: pageSize
});
assert(finalRevision.revision.toString() !== "", "expected a non-empty revision");
assert(finalRevision.progress === "complete", "expected completed delete-all");
assert(finalRevision.deletedCount === BigInt(totalRels-pageSize), "expected cumulative count");
const afterAutoPage = await countRemaining();
console.log(`delete-all: ${afterBounded - afterAutoPage} deleted, ${afterAutoPage} remaining`);
assert(afterAutoPage === 0, `expected every remaining relationship deleted, but ${afterAutoPage} are left`);
// Release the underlying transport now that this example is done with it.
client.close();
console.log("delete_relationships_autopage: PASS");
