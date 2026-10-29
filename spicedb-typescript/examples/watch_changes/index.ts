import { ZedToken } from "../../src/index.js";
import { WatchContent, TransportSecurity, CancelledError, Code } from "../../src/index.js";
/**
 * Example: Watching for changes
 *
 * Demonstrates watching for relationship changes with a bounded consumer that
 * cancels the stream explicitly when it is done.
 *
 * Watch is an open-ended server stream. Break/return release it automatically;
 * an AbortSignal also interrupts a pending next(). This example:
 *
 *   1. subscribes from a known revision,
 *   2. makes a write that must produce a specific update,
 *   3. consumes until it has observed exactly that update,
 *   4. aborts the stream through `options.signal`, and
 *   5. requires the consumer to settle promptly afterwards.
 *
 * Step 5 is the assertion that carries root DESIGN.md, "RULE: Abandoning a
 * stream must release it": if the signal were not wired into the Connect call,
 * the iterator would stay parked on a stream with no further events and this
 * example would fail on RELEASE_TIMEOUT_MS instead of quietly passing.
 */
import { createSpiceDBClient, Transaction, relationship, type WatchChange, } from "../../src/index.js";
function assert(condition: boolean, message: string): void {
    if (!condition) {
        console.error(`ASSERTION FAILED: ${message}`);
        process.exit(1);
    }
}
/** Bounds the wait for the update this example wrote to come back. */
const UPDATE_TIMEOUT_MS = 30000;
/**
 * Bounds how long the consumer may take to settle after the stream is aborted.
 * A released stream rejects immediately; a leaked one never wakes, because a
 * quiet watch stream has nothing else to deliver.
 */
const RELEASE_TIMEOUT_MS = 10000;
/** Rejects after `ms`, so a hang becomes a failure with a message. */
function failAfter(ms: number, message: string): Promise<never> {
    return new Promise((_resolve, reject) => {
        const timer = setTimeout(() => reject(new Error(message)), ms);
        // Do not hold the event loop open on the happy path.
        timer.unref?.();
    });
}
// Endpoint and token come from the environment so the example runs against
// whichever SpiceDB the caller started; the defaults match
// docker-compose.test.yml.
const endpoint = process.env.SPICEDB_ENDPOINT || "localhost:50051";
const token = process.env.SPICEDB_TOKEN || "testtoken";
const client = createSpiceDBClient(endpoint, token, {
    transportSecurity: TransportSecurity.Plaintext
});
// The same schema the other examples write, so this one does not narrow it out
// from under them (they share one SpiceDB).
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
// Clear first. A TOUCH of a relationship that already exists, unchanged, is
// not a change: SpiceDB emits no watch event for it, so leaving a previous
// run's data in place would make this example wait for an update the server
// has no reason to send.
await client.deleteRelationships({ resourceType: "document" }, {
    limit: 1000
});
// A seed write fixes the revision to watch from, so the stream cannot replay
// what earlier examples left behind and cannot miss the write made below.
const seed = new Transaction();
seed.touch(relationship("document:watched", "viewer", "user:seed"));
const { revision: startRevision } = await client.write(seed);
assert(startRevision.toString() !== "", "expected a non-empty seed revision");
// `content: [WatchContent.Relationships, WatchContent.Checkpoints]` asks the server for periodic checkpoint events in
// addition to relationship updates -- recommended behind a proxy that aborts
// idle connections, since a checkpoint keeps the stream alive when nothing has
// changed. A checkpoint carries no changes, so a consumer must branch on
// `event.isCheckpoint` to tell "nothing changed, here is a fresh resume point"
// from "here are changes".
const abortController = new AbortController();
let observedUpdate: WatchChange | undefined;
let observedCheckpointRevision: ZedToken | undefined;
let resumeToken: ZedToken | undefined;
let resolveObserved: () => void = () => { };
const observed = new Promise<void>((resolve) => {
    resolveObserved = resolve;
});
console.log("Watching for document relationship changes...");
const consumer = (async () => {
    for await (const event of client.watch({
        relationshipFilters: [{ resourceType: "document" }],
        startRevision,
        content: [WatchContent.Relationships, WatchContent.Checkpoints]
    }, {
        signal: abortController.signal
    })) {
        // `event.changesThrough` is a resume point for a later `watch({ startRevision })`
        // call -- keep it if this consumer needs to survive a dropped stream
        // without reprocessing everything since the original token or silently
        // losing changes by restarting from head.
        resumeToken = event.changesThrough;
        if (event.isCheckpoint) {
            // A checkpoint carries no changes -- it exists to advertise a fresh
            // resume point and to keep the stream alive during quiet periods.
            assert(event.updates.length === 0, "a checkpoint carries no changes, but this one had some");
            console.log(`[checkpoint] revision: ${event.changesThrough}`);
            observedCheckpointRevision ??= event.changesThrough;
        }
        // Schema notifications can be requested with WatchContent.Schema.
        if (event.schemaUpdated) {
            console.log(`[schema updated] at revision: ${event.changesThrough}`);
        }
        for (const change of event.updates) {
            console.log(`[${change.operation}] ${change.relationship.resource.objectType}:${change.relationship.resource.objectId}#${change.relationship.relation} -> ${change.relationship.subject.object.objectType}:${change.relationship.subject.object.objectId}`);
            if (change.relationship.resource.objectType === "document" &&
                change.relationship.resource.objectId === "watched" &&
                change.relationship.relation === "editor" &&
                change.relationship.subject.object.objectId === "bob") {
                observedUpdate ??= change;
            }
        }
        if (observedUpdate !== undefined && observedCheckpointRevision !== undefined) {
            resolveObserved();
        }
    }
})();
// Keep the process from dying on the consumer's eventual abort rejection
// before it is awaited below.
const consumerSettled = consumer.then(() => undefined, (err: unknown) => err);
// The write the consumer above is waiting for.
const txn = new Transaction();
txn.touch(relationship("document:watched", "editor", "user:bob"));
await client.write(txn);
await Promise.race([
    observed,
    failAfter(UPDATE_TIMEOUT_MS, `did not observe document:watched#editor@user:bob and a checkpoint within ${UPDATE_TIMEOUT_MS}ms`),
]);
assert(observedUpdate !== undefined, "expected the watch stream to deliver the relationship just written");
assert(observedUpdate!.operation === "touch" || observedUpdate!.operation === "create", `expected a create or touch for the relationship just written, got ${observedUpdate!.operation}`);
assert(observedUpdate!.relationship.subject.object.objectType === "user", "expected the update's subject type to survive the conversion");
assert(observedCheckpointRevision !== undefined, "expected at least one checkpoint event -- includeCheckpoints did not reach the server");
assert(resumeToken !== undefined && resumeToken.toString() !== "", "expected a resume token");
// Abandon the stream explicitly. `abort()` is called *before* leaving the
// loop, which is what releases the underlying HTTP/2 stream on this transport.
abortController.abort();
const settled = await Promise.race([
    consumerSettled,
    failAfter(RELEASE_TIMEOUT_MS, `the watch consumer had not settled ${RELEASE_TIMEOUT_MS}ms after abort(): aborting the signal did not release the stream`),
]);
// Caller cancellation is the client's CancelledError (code Canceled, named
// "AbortError"), with signal.reason as cause -- never a platform DOMException.
assert(settled instanceof CancelledError && settled.code === Code.Canceled && settled.name === "AbortError", `expected the aborted watch to reject with CancelledError, got: ${String(settled)}`);
console.log(`watch stream released after abort: ${String(settled)}`);
// Clean up so later examples that write a narrower schema aren't blocked by
// leftover relationships (examples run in sequence against one shared SpiceDB
// instance).
await client.deleteRelationships({ resourceType: "document" }, {
    limit: 1000
});
// Release the underlying transport now that this example is done with it.
client.close();
console.log("watch_changes: PASS");
