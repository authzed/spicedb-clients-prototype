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
  FailedPreconditionError,
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

// Clear before writing this narrower schema, and before the write below: a
// TOUCH of a relationship that already exists, unchanged, is not a change, so
// SpiceDB would emit no watch event for it and the read-back would wait
// forever on a rerun. On a fresh server there is no `document` definition yet,
// which SpiceDB reports as FailedPrecondition -- the one tolerated error.
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

// A seed write fixes the revision the watch below starts from, so it sees the
// metadata write and nothing that came before it.
const seed = new Transaction();
seed.touch(relationship("document:ledger", "viewer", "user:seed"));
const seedRevision = await client.write(seed);

// ── 1. A proto field the idiomatic API does not expose ───────────────────
//
// `raw()` hands back the same client the idiomatic methods call through, so
// this write goes over the same connection and carries the same bearer token
// (set by a transport interceptor — nothing extra to pass).
// Sending the metadata proves nothing on its own: a client that dropped the
// field would look identical from here, because WriteRelationships does not
// echo it back. The only place it becomes observable is the Watch stream, so
// the read-back below is what makes this example able to fail. Watch is
// started before the write so the event cannot be missed.
const metadataAbort = new AbortController();
const metadataSeen = (async (): Promise<Record<string, unknown> | undefined> => {
  for await (const event of client.watch({
    objectTypes: ["document"],
    startRevision: seedRevision,
    signal: metadataAbort.signal,
  })) {
    if (event.metadata !== undefined) {
      return event.metadata as Record<string, unknown>;
    }
  }
  return undefined;
})();

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

// Read the metadata back out of the Watch stream.
const metadataWatchdog = new Promise<never>((_resolve, reject) => {
  const timer = setTimeout(
    () =>
      reject(
        new Error(
          "no watch event carried optionalTransactionMetadata within 30s: the " +
            "metadata sent on the raw write never reached the server",
        ),
      ),
    30_000,
  );
  timer.unref?.();
});
const metadata = await Promise.race([metadataSeen, metadataWatchdog]);
metadataAbort.abort();
console.log(`watch reported transaction metadata: ${JSON.stringify(metadata)}`);
assert(
  metadata?.correlation_id === "example-42",
  `expected correlation_id "example-42" on the watched transaction, got ${String(metadata?.correlation_id)}`,
);
assert(
  metadata?.actor === "billing-job",
  `expected actor "billing-job" on the watched transaction, got ${String(metadata?.actor)}`,
);

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
// ...including the seed written above, which the transaction above does not
// name.
await client.deleteRelationships({ resourceType: "document" });

// Close the CLIENT, never `client.raw().close()` — the raw object is this
// client's own connection, and closing it there breaks every later call.
client.close();
