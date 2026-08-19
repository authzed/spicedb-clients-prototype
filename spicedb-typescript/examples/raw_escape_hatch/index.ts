/**
 * Example: reaching past the idiomatic API with `raw()`
 *
 * Every wrapper eventually meets a request the wrapper does not express. This
 * client's answer is `raw()`: the underlying `SpiceDBProtoClient`, with the
 * four generated Connect clients it makes its own calls through — a workaround
 * short of forking the library. Root DESIGN.md, "What NOT To Do", allows
 * exactly this as "clearly marked secondary API".
 *
 * The gaps demonstrated here are real, not hypothetical:
 *
 *   1. `WriteRelationshipsRequest.optionalTransactionMetadata` is a proto field
 *      this client does not surface anywhere. Applications use it to stamp an
 *      audit correlation ID onto a write, which comes back out of the Watch
 *      stream.
 *   2. `CheckPermission` — the single-check RPC. The idiomatic
 *      `checkPermission()` routes every check through `CheckBulkPermissions`,
 *      so the raw client is how you drive the unary RPC itself.
 *
 * What you give up on the raw path, and why the idiomatic methods stay the
 * default: no `SpiceDBError` mapping (you catch Connect's `ConnectError`), no
 * retry on a transient failure, and no `defaultTimeoutMs` — pass `timeoutMs`
 * yourself or the call is unbounded.
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

const client = createSpiceDBClient("localhost:50051", "testtoken", {
  insecure: true,
});

await client.writeSchema(`
definition user {}

definition document {
  relation viewer: user
  permission view = viewer
}
`);

// ── 1. A proto field the idiomatic API does not expose ───────────────────
//
// `raw()` hands back the same client the idiomatic methods call through, so
// this write goes over the same connection and carries the same bearer token
// (set by a transport interceptor — nothing extra to pass).
const written = await client.raw().permissions.writeRelationships({
  updates: [
    {
      operation: 2, // TOUCH
      relationship: {
        resource: { objectType: "document", objectId: "ledger" },
        relation: "viewer",
        subject: { object: { objectType: "user", objectId: "jimmy" } },
      },
    },
  ],
  optionalTransactionMetadata: {
    correlation_id: "example-42",
    actor: "billing-job",
  },
});
const revision = written.writtenAt?.token ?? "";
console.log(`raw write committed at revision ${revision}`);
assert(revision !== "", "expected a revision from the raw write");

// The idiomatic API picks up right where the raw call left off — same client,
// same connection.
const idiomatic = await client.checkPermission(full(), {
  resourceType: "document",
  resourceId: "ledger",
  permission: "view",
  subjectType: "user",
  subjectId: "jimmy",
});
console.log(`user:jimmy can view document:ledger: ${idiomatic.hasPermission()}`);
assert(idiomatic.hasPermission(), "expected jimmy to have view permission");

// ── 2. An RPC the idiomatic API routes around ────────────────────────────
//
// A raw call gets no client default deadline — pass one yourself.
const single = await client.raw().permissions.checkPermission(
  {
    consistency: { requirement: { case: "fullyConsistent", value: true } },
    resource: { objectType: "document", objectId: "ledger" },
    permission: "view",
    subject: { object: { objectType: "user", objectId: "jimmy" } },
  },
  { timeoutMs: 30_000 },
);
console.log(`raw CheckPermission permissionship: ${single.permissionship}`);
assert(single.permissionship === 2, "expected PERMISSIONSHIP_HAS_PERMISSION");

// Clean up so later examples aren't blocked by leftover relationships.
const cleanup = new Transaction();
cleanup.delete(relationship("document:ledger", "viewer", "user:jimmy"));
await client.write(cleanup);

// Close the CLIENT, never `client.raw().close()` — the raw object is this
// client's own connection, and closing it there breaks every later call.
client.close();
