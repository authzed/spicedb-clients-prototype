/**
 * Example: Schema read/write
 *
 * Demonstrates reading and writing the SpiceDB schema.
 */
import { createSpiceDBClient } from "../../src/index.js";

function assert(condition: boolean, message: string): void {
  if (!condition) {
    console.error(`ASSERTION FAILED: ${message}`);
    process.exit(1);
  }
}

const client = createSpiceDBClient("localhost:50051", "testtoken", {
  insecure: true,
});

// Write a new schema
const newSchema = `
definition user {}

definition document {
  relation viewer: user
  relation editor: user
  relation owner: user

  permission view = viewer + editor + owner
  permission edit = editor + owner
  permission delete = owner
}
`;

const writeRevision = await client.writeSchema(newSchema);
console.log(`Schema written at revision: ${writeRevision}`);
assert(writeRevision !== "", "expected non-empty write revision");

// Verify by reading back
const { schema: updated, revision } = await client.readSchema();
console.log(`Read schema at revision ${revision}:`);
console.log(updated);

assert(updated.includes("definition user"), "expected schema to contain 'definition user'");
assert(updated.includes("definition document"), "expected schema to contain 'definition document'");

console.log("schema_management: PASS");
