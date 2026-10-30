import { TransportSecurity, Retries } from "../modes.js";
/**
 * The idiomatic client's `tls` option, proven end-to-end.
 *
 * The proto tier holds the fuller fixture set (`custom-tls.test.ts` in
 * spicedb-typescript-proto, including mutual TLS and the guard shapes). What
 * these tests pin is that the public constructor actually reaches it: `tls`
 * has to travel from `SpiceDBClientOptions` through `createProtoClient` into
 * the `Http2SessionManager`, and a dropped field anywhere along that path is
 * invisible to a unit test that only inspects options.
 *
 * Root DESIGN.md, "RULE: A system-TLS constructor must reach a real server",
 * permits delegating to Node's bundled Mozilla root store *because* a caller
 * can supply their own material when that is not enough -- a CA an operator
 * installed in the host's own store is not honoured otherwise. These are the
 * tests that make that justification true rather than asserted.
 */
import { describe, it, expect, beforeAll, afterAll } from "vitest";
import { execFileSync } from "node:child_process";
import { generateKeyPairSync } from "node:crypto";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import * as http2 from "node:http2";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { connectNodeAdapter } from "@connectrpc/connect-node";
import { CheckPermissionResponse_Permissionship, PermissionsService } from "@spicedb/proto";
import { SpiceDBClient } from "../client.js";
import { full } from "../consistency.js";
import { InvalidArgumentError } from "../errors.js";
const TOKEN = "test-token";
/**
 * One real check. `checkBulk` with zero checks no longer reaches the
 * wire at all (an empty bulk check is a round trip whose only possible
 * answer is `[]`), and a call that never reaches the wire cannot prove a
 * handshake completed -- which is the whole point of these tests.
 */
const ONE_CHECK = {
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
let dir: string;
let ca: Buffer;
let serverCert: Buffer;
let serverKey: Buffer;
function openssl(...args: string[]): void {
    // Throws rather than skipping if `openssl` is missing: a skipped test reads
    // as coverage while proving nothing.
    execFileSync("openssl", args, { stdio: "pipe" });
}
beforeAll(() => {
    dir = mkdtempSync(join(tmpdir(), "spicedb-client-tls-"));
    openssl("req", "-x509", "-newkey", "rsa:2048", "-nodes", "-keyout", join(dir, "ca.key"), "-out", join(dir, "ca.crt"), "-days", "1", "-subj", "/CN=spicedb-typescript test CA", "-addext", "basicConstraints=critical,CA:TRUE");
    // SAN, not CN: Node's TLS stack ignores the common name entirely.
    writeFileSync(join(dir, "server.ext"), "basicConstraints=CA:FALSE\nkeyUsage=digitalSignature,keyEncipherment\n" +
        "subjectAltName=DNS:localhost,IP:127.0.0.1\nextendedKeyUsage=serverAuth\n");
    openssl("req", "-newkey", "rsa:2048", "-nodes", "-keyout", join(dir, "server.key"), "-out", join(dir, "server.csr"), "-subj", "/CN=localhost");
    openssl("x509", "-req", "-in", join(dir, "server.csr"), "-CA", join(dir, "ca.crt"), "-CAkey", join(dir, "ca.key"), "-CAcreateserial", "-out", join(dir, "server.crt"), "-days", "1", "-extfile", join(dir, "server.ext"));
    ca = readFileSync(join(dir, "ca.crt"));
    serverCert = readFileSync(join(dir, "server.crt"));
    serverKey = readFileSync(join(dir, "server.key"));
}, 60000);
afterAll(() => {
    rmSync(dir, { recursive: true, force: true });
});
async function serve(): Promise<{
    port: number;
    close: () => Promise<void>;
}> {
    const handler = connectNodeAdapter({
        routes: (router) => {
            router.service(PermissionsService, {
                // One pair per request item: the client's pair-count guard rejects
                // a response whose pair count does not match the request's, so a
                // fixed empty response would fail any caller that asks about an
                // actual check.
                checkBulkPermissions: (req) => ({
                    pairs: req.items.map(item => ({
                        request: item,
                        response: { case: "item" as const, value: { permissionship: CheckPermissionResponse_Permissionship.HAS_PERMISSION } }
                    }))
                })
            });
        }
    });
    const server = http2.createSecureServer({ key: serverKey, cert: serverCert }, handler);
    await new Promise<void>((resolve) => server.listen(0, "localhost", resolve));
    const address = server.address();
    if (address === null || typeof address === "string") {
        throw new Error("expected a TCP address from the test server");
    }
    return {
        port: address.port,
        close: () => new Promise<void>((resolve) => server.close(() => resolve()))
    };
}
describe("SpiceDBClient tls option", () => {
    it("carries a private CA all the way to a completed handshake", async () => {
        const { port, close } = await serve();
        try {
            const trusting = new SpiceDBClient({
                endpoint: `localhost:${port}`,
                token: TOKEN,
                tls: { caCert: ca },
                retries: Retries.Disabled
            });
            // One real check, not an empty array: `checkBulk` with zero
            // checks now sends no request at all, so an empty call would resolve
            // without ever completing the handshake this test exists to prove.
            await expect(trusting.checkBulk([ONE_CHECK].map(request => ({
                ...request,
                consistency: full()
            })))).resolves.toHaveLength(1);
            trusting.close();
            // Same server, same call, no `tls`: Node's bundled roots have never
            // heard of the CA above -- exactly the position an operator behind a
            // private CA is in, and the reason the option had to exist.
            const untrusting = new SpiceDBClient({
                endpoint: `localhost:${port}`,
                token: TOKEN,
                retries: Retries.Disabled
            });
            await expect(untrusting.checkBulk([ONE_CHECK].map(request => ({
                ...request,
                consistency: full()
            })))).rejects.toThrow();
            untrusting.close();
        }
        finally {
            await close();
        }
    }, 30000);
    it("refuses trust material combined with insecure, at construction", () => {
        // Supplying a CA must not become a quieter route to a plaintext channel:
        // node:tls would drop it silently and the bearer token would go out in
        // cleartext behind a call site that reads as though TLS were configured.
        // Root DESIGN.md, "RULE: Credentials over insecure transport require an
        // explicit opt-in".
        expect(() => new SpiceDBClient({
            endpoint: "localhost:50051",
            token: TOKEN,
            transportSecurity: TransportSecurity.Plaintext,
            tls: { caCert: "pem" }
        })).toThrow(/transportSecurity: TransportSecurity.Plaintext and TLS material/);
    });
    it("keeps the non-loopback refusal ahead of the TLS one", () => {
        expect(() => new SpiceDBClient({
            endpoint: "evil.example.com:443",
            token: TOKEN,
            transportSecurity: TransportSecurity.Plaintext,
            tls: { caCert: "pem" }
        })).toThrow(/remoteCredentials/);
    });
    // Root DESIGN.md, "Reject unusable credentials and trust material at
    // construction": node:tls silently accepts a non-PEM `ca` and then trusts
    // nothing, so without this check the first call fails as a bare handshake
    // error that never names the input.
    it.each([
        ["caCert not PEM", { caCert: "not a certificate" }, /tls\.caCert is not a PEM-encoded X\.509 certificate/],
        ["caCert with a corrupt body", { caCert: "-----BEGIN CERTIFICATE-----\nAAAA\n-----END CERTIFICATE-----\n" }, /tls\.caCert is not a PEM-encoded X\.509 certificate/],
        ["clientCert not PEM", { clientCert: "garbage", clientKey: "use-real" }, /tls\.clientCert is not a PEM-encoded X\.509 certificate/],
        ["clientKey not PEM", { clientCert: "use-real", clientKey: "garbage" }, /tls\.clientKey is not a PEM-encoded private key/],
    ])("refuses unparseable %s at construction, naming the input", (_name, tls, message) => {
        const material = Object.fromEntries(Object.entries(tls).map(([k, v]) => [k, v === "use-real" ? (k === "clientKey" ? serverKey : serverCert) : v]));
        let thrown: unknown;
        try {
            new SpiceDBClient({ endpoint: "localhost:50116", token: TOKEN, tls: material });
        }
        catch (error) {
            thrown = error;
        }
        expect(thrown).toBeInstanceOf(InvalidArgumentError);
        expect((thrown as Error).message).toMatch(message);
    });
    it("accepts parseable CA and client identity material", () => {
        const client = new SpiceDBClient({ endpoint: "localhost:50116", token: TOKEN, tls: { caCert: ca, clientCert: serverCert, clientKey: serverKey } });
        client.close();
    });
    // Root DESIGN.md: a client key that does not match its certificate fails at
    // construction naming both inputs, not as a handshake error on the first call.
    it.each([
        ["an RSA key from another pair (the CA's)", () => readFileSync(join(dir, "ca.key"))],
        ["an EC key", () => generateKeyPairSync("ec", { namedCurve: "prime256v1" }).privateKey.export({ type: "pkcs8", format: "pem" })],
    ])("refuses a client certificate paired with %s", (_name, otherKey) => {
        let thrown: unknown;
        try {
            new SpiceDBClient({ endpoint: "localhost:50116", token: TOKEN, tls: { caCert: ca, clientCert: serverCert, clientKey: otherKey() } });
        }
        catch (error) {
            thrown = error;
        }
        expect(thrown).toBeInstanceOf(InvalidArgumentError);
        expect((thrown as Error).message).toMatch(/tls\.clientKey does not match tls\.clientCert/);
    });
    it.each(["", " ", "\t\n"])("refuses a blank token %j at construction", (token) => {
        for (const transportSecurity of [TransportSecurity.Tls, TransportSecurity.Plaintext]) {
            let thrown: unknown;
            try {
                new SpiceDBClient({ endpoint: "localhost:50116", token, transportSecurity });
            }
            catch (error) {
                thrown = error;
            }
            expect(thrown).toBeInstanceOf(InvalidArgumentError);
            expect((thrown as Error).message).toMatch(/token must be a nonempty string/);
        }
    });
});
