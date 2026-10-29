import { TransportSecurity } from "../modes.js";
import { describe, expect, it } from "vitest";
import * as http2 from "node:http2";
import { connectNodeAdapter } from "@connectrpc/connect-node";
import { PermissionsService } from "@spicedb/proto";
import { SpiceDBClient } from "../client.js";
import { full } from "../consistency.js";
import { CancelledError } from "../errors.js";
async function serve() {
    let closeStream!: () => void;
    const streamClosed = new Promise<void>(resolve => closeStream = resolve);
    const sessions = new Set<http2.ServerHttp2Session>();
    const server = http2.createServer(connectNodeAdapter({ routes: router => router.service(PermissionsService, {
            async *readRelationships(_request, context) {
                yield { relationship: { resource: { objectType: "document", objectId: "a" }, relation: "viewer", subject: { object: { objectType: "user", objectId: "a" } } } };
                if (!context.signal.aborted)
                    await new Promise<void>(resolve => context.signal.addEventListener("abort", () => resolve(), { once: true }));
            },
        }) }));
    server.on("session", session => { sessions.add(session); session.on("close", () => sessions.delete(session)); });
    server.on("stream", stream => stream.on("close", closeStream));
    await new Promise<void>((resolve, reject) => { server.once("error", reject); server.listen(0, "127.0.0.1", resolve); });
    const address = server.address();
    if (!address || typeof address === "string")
        throw new Error("missing TCP address");
    return { endpoint: `127.0.0.1:${address.port}`, streamClosed, close: async () => {
            for (const session of sessions)
                session.destroy();
            await new Promise<void>(resolve => server.close(() => resolve()));
        } };
}
async function promptly<T>(promise: Promise<T>): Promise<T> {
    let timer: ReturnType<typeof setTimeout> | undefined;
    try {
        return await Promise.race([promise, new Promise<never>((_, reject) => timer = setTimeout(() => reject(new Error("server-side HTTP/2 stream remained active")), 1500))]);
    }
    finally {
        clearTimeout(timer);
    }
}
describe("real HTTP/2 stream release", () => {
    it.each(["break", "manual return", "abort pending next", "return pending next", "close pending next"] as const)("releases transport after %s", async (mode) => {
        const server = await serve();
        const client = new SpiceDBClient({ endpoint: server.endpoint, token: "t", transportSecurity: TransportSecurity.Plaintext });
        const controller = new AbortController();
        try {
            const stream = client.readRelationships({ filter: { resourceType: "document" }, consistency: full() }, { signal: controller.signal });
            expect(Symbol.asyncIterator in stream).toBe(true);
            expect("then" in stream).toBe(false);
            if (mode === "break") {
                for await (const value of stream) {
                    expect(value.resource.objectId).toBe("a");
                    break;
                }
            }
            else {
                expect((await stream.next()).done).toBe(false);
                if (mode === "manual return")
                    await promptly(stream.return!());
                else {
                    const pending = stream.next();
                    const settled = pending.then(() => undefined, error => error as unknown);
                    if (mode === "abort pending next")
                        controller.abort("caller");
                    else if (mode === "return pending next")
                        await promptly(stream.return!());
                    else
                        client.close();
                    const error = await promptly(settled);
                    expect(error).toBeInstanceOf(CancelledError);
                    expect((error as CancelledError).name).toBe("AbortError");
                    if (mode === "abort pending next")
                        expect((error as Error).cause).toBe("caller");
                }
            }
            // The client stays open here for break/return/abort: only iterator cleanup can release this stream.
            await promptly(server.streamClosed);
            if (mode !== "close pending next")
                expect(controller.signal.aborted).toBe(mode === "abort pending next");
        }
        finally {
            client.close();
            await server.close();
        }
    });
});
