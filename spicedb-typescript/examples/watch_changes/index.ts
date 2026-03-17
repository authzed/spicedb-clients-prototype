/**
 * Example: Watching for changes
 *
 * Demonstrates watching for relationship changes using the Watch API.
 */
import { createSpiceDBClient } from "../../src/index.js";

const client = createSpiceDBClient("localhost:50051", "testtoken", {
  insecure: true,
});

// Watch all changes
console.log("Watching for all relationship changes...");
for await (const event of client.watch()) {
  if (event.isCheckpoint) {
    console.log(`[checkpoint] revision: ${event.revision}`);
    continue;
  }

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
}

// Watch only specific object types (commented out since the above loop is infinite)
// for await (const event of client.watch({ objectTypes: ["document"] })) {
//   // Only document changes
// }

// Resume from a specific revision (commented out)
// for await (const event of client.watch({ startRevision: "some-token" })) {
//   // Changes since that revision
// }
