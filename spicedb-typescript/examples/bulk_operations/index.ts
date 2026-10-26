import { TransportSecurity, SubjectRelationKind } from "../../src/index.js";
import * as http2 from "node:http2";
import { connectNodeAdapter } from "@connectrpc/connect-node";
import { RelationshipsService, WatchPermissionSetsService, RoaringLookupResourcesService } from "@spicedb/proto";
/**
 * Example: Bulk checks, bulk import, and bulk export
 *
 * Demonstrates bulk permission checks, bulk relationship import via
 * importRelationships, and bulk relationship export.
 */
import { createSpiceDBClient, Transaction, relationship, full, atLeast, type CheckRequest, } from "../../src/index.js";
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
txn.touch(relationship("document:design", "editor", "user:sally"));
const { revision: setupRevision } = await client.write(txn);
// Bulk check permissions. Inputs over 1,000 checks are split into one
// CheckBulkPermissions request per 1,000 automatically and the results
// concatenated in input order -- SpiceDB rejects a single request carrying
// more than 10,000. Nothing here changes for a larger array.
const checks: CheckRequest[] = [
    {
        permission: "view",
        resource: {
            objectType: "document",
            objectId: "readme"
        },
        subject: {
            object: {
                objectType: "user",
                objectId: "jimmy"
            }
        },
        consistency: full()
    },
    {
        permission: "edit",
        resource: {
            objectType: "document",
            objectId: "design"
        },
        subject: {
            object: {
                objectType: "user",
                objectId: "sally"
            }
        },
        consistency: full()
    },
];
const results = await client.checkBulk(checks.map(request => ({
    ...request,
    consistency: atLeast(setupRevision)
})));
for (let i = 0; i < checks.length; i++) {
    const c = checks[i];
    console.log(`${c.subject.object.objectType}:${c.subject.object.objectId} ${c.permission} ${c.resource.objectType}:${c.resource.objectId}: ` +
        `${results[i].hasPermission()} (permissionship: ${results[i].permissionship})`);
    assert(results[i].hasPermission(), `expected check ${i} to have permission`);
}
// Bulk import: load many relationships in a single streaming call
const bulkRels = [
    relationship("document:readme", "owner", "user:admin"),
    relationship("document:design", "owner", "user:admin"),
    relationship("document:notes", "viewer", "user:jimmy"),
];
const numLoaded = await client.importRelationships(bulkRels);
console.log(`\nBulk-imported ${numLoaded} relationships`);
assert(numLoaded === BigInt(bulkRels.length), `expected ${bulkRels.length} relationships imported, got ${numLoaded}`);
// The array above is fine when the data is already in memory. For a dataset
// bigger than memory -- which is what a bulk import is for -- hand in a
// generator instead: relationships are converted and batched as they are
// pulled, so only one batch is ever resident. An async generator works the
// same way, which is what reading from a DB cursor or a file stream looks
// like in practice.
function* generatedRels(count: number) {
    for (let i = 0; i < count; i++) {
        yield relationship(`document:generated${i}`, "viewer", "user:sally");
    }
}
const numGenerated = await client.importRelationships(generatedRels(5));
console.log(`Bulk-imported ${numGenerated} relationships from a generator`);
assert(numGenerated === 5n, `expected 5 relationships imported from the generator, got ${numGenerated}`);
// Export all document relationships (includes both the initial write and
// the bulk import above)
console.log("\nExporting all document relationships:");
let exportCount = 0;
for await (const rel of client.exportRelationships({
    consistency: full(),
    filter: {
        resourceType: "document"
    }
})) {
    console.log(`  ${rel.resource.objectType}:${rel.resource.objectId}#${rel.relation} -> ${rel.subject.object.objectType}:${rel.subject.object.objectId}`);
    exportCount++;
}
const expectedExportCount = 2 + bulkRels.length;
assert(exportCount >= expectedExportCount, `expected at least ${expectedExportCount} exported relationships, got ${exportCount}`);
// any aggregate / all aggregate
const anyAllowed = await client.checkBulk(checks.map(request => ({
    ...request,
    consistency: atLeast(setupRevision)
}))).then(results => results.length > 0 && results.some(result => result.hasPermission()));
console.log(`\nAny permission granted: ${anyAllowed}`);
assert(anyAllowed, "expected at least one permission granted");
const allAllowed = await client.checkBulk(checks.map(request => ({
    ...request,
    consistency: atLeast(setupRevision)
}))).then(results => results.length > 0 && results.every(result => result.hasPermission()));
console.log(`All permissions granted: ${allAllowed}`);
assert(allAllowed, "expected all permissions granted");
// Clean up so later examples that write a narrower schema aren't blocked by
// leftover relationships (examples run in sequence against one shared
// SpiceDB instance).
await client.deleteRelationships({ resourceType: "document" }, {
    limit: 1000
});
// Release the underlying transport now that this example is done with it.
client.close();
// Materialize is a separate service. This local stand-in makes the native
// experimental shapes executable even when CI starts only ordinary SpiceDB.
const materializeServer = http2.createServer(connectNodeAdapter({routes:router=>{
 const authenticate=(authorization:string|null)=>assert(authorization==="Bearer materialize-demo","expected authenticated shared transport");
 router.service(RelationshipsService,{experimentalCountRelationshipsByFilter(request,context){
  authenticate(context.requestHeader.get("authorization"));
  assert(request.relationshipFilter?.optionalSubjectFilter?.optionalRelation?.relation==="","expected exact direct-subject filter");
  return {relationshipCount:2n,readAt:{token:"snapshot"}};
 }});
 router.service(WatchPermissionSetsService,{
  async *watchPermissionSets(request,context){authenticate(context.requestHeader.get("authorization"));assert(request.optionalStartingAfter?.token==="snapshot","expected resumable snapshot token");yield {response:{case:"completedRevision",value:{token:"snapshot"}}};},
  async *lookupPermissionSets(_request,context){authenticate(context.requestHeader.get("authorization"));yield {change:{atRevision:{token:"snapshot"},operation:1,parentSet:{objectType:"document",objectId:"a",permissionOrRelation:"view"},child:{case:"childMember",value:{objectType:"user",objectId:"alice"}}},cursor:{limit:1000,startingIndex:1,completedMembers:true,startingKey:"last",cursor:"opaque",token:{token:"snapshot"}}};},
  downloadPermissionSets(_request,context){authenticate(context.requestHeader.get("authorization"));return {files:[{name:"snapshot.avro",url:"https://download.invalid/snapshot.avro"}],atRevision:{token:"snapshot"},timestamp:{seconds:1767225600n,nanos:0}};},
 });
 router.service(RoaringLookupResourcesService,{experimentalRoaringLookupResources(_request,context){authenticate(context.requestHeader.get("authorization"));return {bitmap:new Uint8Array([1,2,3]),cardinality:2n,atRevision:{token:"snapshot"}};}});
}}));
await new Promise<void>((resolve,reject)=>{materializeServer.once("error",reject);materializeServer.listen(0,"127.0.0.1",resolve);});
const materializeAddress=materializeServer.address();
if(!materializeAddress||typeof materializeAddress==="string")throw new Error("missing Materialize test address");
const materialize=createSpiceDBClient(`127.0.0.1:${materializeAddress.port}`,"materialize-demo",{transportSecurity:TransportSecurity.Plaintext});
try {
 const count=await materialize.experimentalCountRelationshipsByFilter({resourceType:"document",subject:{subjectType:"user",relation:{kind:SubjectRelationKind.Direct}}});
 assert(count.count===2n,"expected bigint filtered count");
 const snapshot=await materialize.experimentalDownloadPermissionSets();
 assert(snapshot.files[0].name==="snapshot.avro"&&snapshot.timestamp?.toISOString()==="2026-01-01T00:00:00.000Z","expected native snapshot metadata");
 let sets=0;
 for await(const set of materialize.experimentalLookupPermissionSets({revision:snapshot.revision})) {assert(set.change?.child.kind==="member"&&set.cursor?.completedMembers===true,"expected native member and complete cursor");sets++;}
 assert(sets===1,"expected one materialized permission set");
 let completed=false;
 for await(const event of materialize.experimentalWatchPermissionSets({startRevision:snapshot.revision})) {completed=event.kind==="completedRevision";break;}
 assert(completed,"expected permission-set completion event");
 const roaring=await materialize.experimentalRoaringLookupResources({resourceType:"document",permission:"view",subject:{object:{objectType:"user",objectId:"alice"}},consistency:full()});
 assert(roaring.cardinality===2n&&roaring.bitmap[0]===1&&roaring.revision.equals(snapshot.revision),"expected native roaring bytes/count/revision");
} finally {materialize.close();await new Promise<void>(resolve=>materializeServer.close(()=>resolve()));}
console.log("bulk_operations: PASS");
