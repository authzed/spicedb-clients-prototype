/**
 * Example: Watching for changes
 *
 * Demonstrates watching for relationship changes using the Watch API.
 */
import { createSpiceDBClient } from "../../src/index.js";

// Endpoint and token come from the environment so the example runs against
// whichever SpiceDB the caller started; the defaults match
// docker-compose.test.yml.
const endpoint = process.env.SPICEDB_ENDPOINT || "localhost:50051";
const token = process.env.SPICEDB_TOKEN || "testtoken";

const client = createSpiceDBClient(endpoint, token, {
  insecure: true,
});

// Watch all changes. `includeCheckpoints: true` asks the server for
// periodic checkpoint events in addition to relationship updates --
// recommended behind a proxy that aborts idle connections, since a
// checkpoint keeps the stream alive even when nothing has changed. Without
// it, the server never sends is_checkpoint events, so `event.isCheckpoint`
// would always be false and the branch below would be unreachable.
console.log("Watching for all relationship changes...");
let resumeToken: string | undefined;
for await (const event of client.watch({ includeCheckpoints: true })) {
  if (event.isCheckpoint) {
    // A checkpoint carries no changes -- it exists to advertise a fresh
    // resume point and to keep the stream alive during quiet periods.
    console.log(`[checkpoint] revision: ${event.revision}`);
    resumeToken = event.revision;
    continue;
  }

  // Unlike isCheckpoint above, this branch is presently unreachable: nothing
  // in this client can yet request WATCH_KIND_INCLUDE_SCHEMA_UPDATES, so the
  // server has no reason to ever send schemaUpdated: true. Left in place as
  // documentation for when that support is added.
  if (event.schemaUpdated) {
    console.log(`[schema updated] at revision: ${event.revision}`);
  }

  for (const change of event.changes) {
    console.log(
      `[${change.operation}] ${change.relationship.resourceType}:${change.relationship.resourceId}#${change.relationship.resourceRelation} -> ${change.relationship.subjectType}:${change.relationship.subjectId}`,
    );
  }

  if (event.metadata) {
    console.log(`  metadata: ${JSON.stringify(event.metadata)}`);
  }

  // `event.revision` is a resume point for a later `watch({ startRevision })`
  // call -- keep it if this consumer needs to survive a dropped stream
  // without reprocessing everything since the original token or silently
  // losing changes by restarting from head.
  resumeToken = event.revision;
}

// Watch only specific object types (commented out since the above loop is infinite)
// for await (const event of client.watch({ objectTypes: ["document"] })) {
//   // Only document changes
// }

// Resume from a specific revision after a dropped stream (commented out)
// for await (const event of client.watch({ startRevision: resumeToken })) {
//   // Changes since that revision, without reprocessing or gaps
// }
console.log(`last resume token seen: ${resumeToken}`);
