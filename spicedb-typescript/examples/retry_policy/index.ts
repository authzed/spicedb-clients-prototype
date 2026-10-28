import { TransportSecurity } from "../../src/index.js";
/**
 * Example: which calls are retried for you, and which deliberately are not
 *
 * Root DESIGN.md, "RULE: Automatic retry is for idempotent operations only".
 *
 * The rule exists because a silently retried mutation produces a confident
 * wrong answer. If a writeRelationships carrying a CREATE commits and the
 * response is lost, the retry comes back ALREADY_EXISTS -- and the caller
 * concludes a write failed that in fact succeeded. Retrying reads is free;
 * retrying mutations is only safe when the caller opted in knowing that.
 *
 * Attempts are counted *server-side*, which is the only way to tell a retry
 * from its absence: from the caller's side a transparently-retried success and
 * a first-try success are identical, and that is exactly the property that
 * would rot unnoticed.
 *
 * It stands up a stand-in SpiceDB because a real one cannot be asked to fail
 * transiently on demand.
 */
import * as http2 from "node:http2";
import { Code, ConnectError } from "@connectrpc/connect";
import { connectNodeAdapter } from "@connectrpc/connect-node";
import { PermissionsService } from "@spicedb/proto";
import { createSpiceDBClient, full, InvalidArgumentError, relationship, ResourceExhaustedError, Transaction, UnavailableError, } from "../../src/index.js";
const DOC = {
    permission: "view",
    resource: {
        objectType: "document",
        objectId: "readme"
    },
    subject: {
        object: {
            objectType: "user",
            objectId: "alice"
        }
    },
    consistency: full()
};
function assert(condition: unknown, message: string): asserts condition {
    if (!condition) {
        throw new Error(message);
    }
}
interface Counts {
    check: number;
    write: number;
}
function countingServer(counts: Counts, checkFailures: number, checkCode: Code): http2.Http2Server {
    const handler = connectNodeAdapter({
        routes: (router) => {
            router.service(PermissionsService, {
                checkPermission: () => {
                    counts.check += 1;
                    if (counts.check <= checkFailures) {
                        throw new ConnectError("transient, from the stand-in", checkCode);
                    }
                    return { permissionship: 2 as never }; // HAS_PERMISSION
                },
                writeRelationships: () => {
                    counts.write += 1;
                    // Always fails, transiently. A retrying client would come back.
                    throw new ConnectError("transient, from the stand-in", Code.Unavailable);
                }
            });
        }
    });
    return http2.createServer(handler);
}
async function listen(server: http2.Http2Server): Promise<number> {
    await new Promise<void>((resolve) => server.listen(0, "localhost", () => resolve()));
    const address = server.address();
    assert(address !== null && typeof address !== "string", "expected a TCP address from the stand-in");
    return address.port;
}
async function main(): Promise<void> {
    // ── 1. A read IS retried, transparently ──────────────────────────────
    //
    // Two UNAVAILABLE responses, then success. The caller sees one successful
    // check and never learns the first two attempts happened -- the entire value
    // of retrying reads, and safe precisely because a repeated read changes
    // nothing.
    {
        const counts: Counts = { check: 0, write: 0 };
        const server = countingServer(counts, 2, Code.Unavailable);
        const port = await listen(server);
        const client = createSpiceDBClient(`localhost:${port}`, "t", { transportSecurity: TransportSecurity.Plaintext });
        try {
            const result = await client.check({
                ...DOC,
                consistency: full()
            });
            assert(result.hasPermission(), "the retried check should have granted");
            assert(counts.check === 3, `expected 2 failures plus 1 success = 3 attempts, got ${counts.check} (0 or 1 means reads are not retried at all)`);
            console.log(`read: failed twice with UNAVAILABLE, retried to success in ${counts.check} attempts`);
        }
        finally {
            client.close();
            await new Promise<void>((resolve) => server.close(() => resolve()));
        }
    }
    // ── 2. A mutation is NOT retried ─────────────────────────────────────
    //
    // The same transient code, on a write. The error reaches the caller on the
    // first attempt, so the caller -- who alone knows whether a replay is safe
    // for the transaction they built -- decides what happens next.
    {
        const counts: Counts = { check: 0, write: 0 };
        const server = countingServer(counts, 0, Code.Unavailable);
        const port = await listen(server);
        const client = createSpiceDBClient(`localhost:${port}`, "t", { transportSecurity: TransportSecurity.Plaintext });
        try {
            const txn = new Transaction();
            txn.touch(relationship("document:readme", "viewer", "user:alice"));
            let caught: unknown;
            try {
                await client.write(txn);
            }
            catch (err) {
                caught = err;
            }
            assert(caught instanceof UnavailableError, `expected the transient failure to surface as UnavailableError: got ${String(caught)}`);
            assert(counts.write === 1, `a mutation must not be retried silently: writeRelationships saw ${counts.write} attempts, so a lost response would leave the caller believing a committed write had failed`);
            console.log(`mutation: failed with UNAVAILABLE and was attempted exactly ${counts.write} time -- not retried`);
        }
        finally {
            client.close();
            await new Promise<void>((resolve) => server.close(() => resolve()));
        }
    }
    // ── 3. RESOURCE_EXHAUSTED is not retryable, even on a read ───────────
    //
    // In SpiceDB this code means memory load-shed or a deterministic
    // MaxDepthExceeded. Retrying the first makes the overload worse; the second
    // can never succeed however many times it is tried.
    {
        const counts: Counts = { check: 0, write: 0 };
        const server = countingServer(counts, 99, Code.ResourceExhausted);
        const port = await listen(server);
        const client = createSpiceDBClient(`localhost:${port}`, "t", { transportSecurity: TransportSecurity.Plaintext });
        try {
            let caught: unknown;
            try {
                await client.check({
                    ...DOC,
                    consistency: full()
                });
            }
            catch (err) {
                caught = err;
            }
            assert(caught instanceof ResourceExhaustedError, `expected ResourceExhaustedError: got ${String(caught)}`);
            assert(counts.check === 1, `RESOURCE_EXHAUSTED must not be retried: saw ${counts.check} attempts, which turns a load-shedding SpiceDB into a client-driven retry storm`);
            console.log(`RESOURCE_EXHAUSTED: attempted exactly ${counts.check} time -- no retry storm`);
        }
        finally {
            client.close();
            await new Promise<void>((resolve) => server.close(() => resolve()));
        }
    }
    // ── 4. An idempotent write may opt in, for one call ───────────────────
    //
    // A touch-only transaction without preconditions converges to the same
    // state however often it is replayed, so the caller may opt that one call
    // into retries. A create (or a preconditioned write, a delete, an import)
    // is refused before any request: replaying it is not safe.
    {
        const counts: Counts = { check: 0, write: 0 };
        const server = countingServer(counts, 0, Code.Unavailable);
        const port = await listen(server);
        const client = createSpiceDBClient(`localhost:${port}`, "t", { transportSecurity: TransportSecurity.Plaintext });
        const retryTwice = { maxRetries: 2, retryableCodes: [Code.Unavailable], initialBackoffMs: 1 };
        try {
            const touched = await client.write(new Transaction().touch(relationship("document:readme", "viewer", "user:alice")), { retryPolicy: retryTwice }).catch((e: unknown) => e);
            assert(touched instanceof UnavailableError, `expected the stand-in's failure after the retries: got ${String(touched)}`);
            assert(counts.write === 3, `an opted-in touch-only write is attempted 1 + 2 times: saw ${counts.write}`);
            const created = await client.write(new Transaction().create(relationship("document:readme", "viewer", "user:alice")), { retryPolicy: retryTwice }).catch((e: unknown) => e);
            assert(created instanceof InvalidArgumentError, `a create must refuse the retry opt-in: got ${String(created)}`);
            assert(counts.write === 3, `the refused create must send nothing: saw ${counts.write} attempts`);
            console.log(`opt-in: touch-only write attempted ${counts.write} times; a create refused the opt-in before any request`);
        }
        finally {
            client.close();
            await new Promise<void>((resolve) => server.close(() => resolve()));
        }
    }
    console.log("retry_policy: reads retried, mutations and load-shed left to the caller");
}
main().catch((err: unknown) => {
    console.error(err);
    process.exit(1);
});
