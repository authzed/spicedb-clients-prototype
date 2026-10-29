import { describe, it, expect } from "vitest";
import { createSpiceDBClient } from "../client.js";
import { InvalidArgumentError, UnavailableError } from "../errors.js";
import { Retries, RemoteCredentials, TransportSecurity } from "../modes.js";
/**
 * Root DESIGN.md, "Accept the endpoint forms deployments use, or reject them
 * at construction": `host:port`, `http://host:port` and `https://host[:port]`
 * build a client; every other form fails at construction with an
 * InvalidArgumentError naming the problem and the accepted forms, before the
 * remote-plaintext policy is consulted.
 */
const FORMS = /expected host:port, http:\/\/host:port \(plaintext\) or https:\/\/host\[:port\] \(TLS\)/;
const plaintext = { transportSecurity: TransportSecurity.Plaintext };
function construct(endpoint: string, options: Parameters<typeof createSpiceDBClient>[2] = plaintext): void {
    createSpiceDBClient(endpoint, "test-token", options).close();
}
describe("endpoint forms", () => {
    it.each(["127.0.0.1:50051", "localhost:50051", "http://127.0.0.1:50051", "HTTP://localhost:50051", "http://[::1]:50051", "http://127.0.0.1:50051/"])("accepts plaintext loopback %s", (endpoint) => {
        expect(() => construct(endpoint)).not.toThrow();
    });
    it.each(["spicedb.example.com:443", "spicedb.example.com", "https://spicedb.example.com", "https://spicedb.example.com:8443", "https://spicedb.example.com/"])("accepts TLS endpoint %s", (endpoint) => {
        expect(() => construct(endpoint, {})).not.toThrow();
    });
    it("lets an http:// URL select plaintext when transportSecurity is omitted", () => {
        expect(() => construct("http://127.0.0.1:50051", {})).not.toThrow();
    });
    it("rejects an http:// URL with an explicit TLS choice", () => {
        expect(() => construct("http://127.0.0.1:50051", { transportSecurity: TransportSecurity.Tls })).toThrowError(/http:\/\/ selects plaintext, but this client was configured for TLS/);
    });
    it("rejects an https:// URL with an explicit plaintext choice", () => {
        expect(() => construct("https://127.0.0.1:50051")).toThrowError(/https:\/\/ selects TLS, but this client was configured for plaintext/);
    });
    it.each([
        ["http://localhost", /http:\/\/ endpoint needs an explicit port/],
        ["localhost", /a plaintext endpoint needs an explicit port, e\.g\. localhost:50051/],
        ["127.0.0.1", /a plaintext endpoint needs an explicit port/],
        ["[::1]", /a plaintext endpoint needs an explicit port/],
        ["::1", /a plaintext endpoint needs an explicit port/],
        ["http://127.0.0.1:50051/x", /a path is not supported/],
        ["http://127.0.0.1:50051?x=1", /a query is not supported/],
        ["http://127.0.0.1:50051#x", /a fragment is not supported/],
        ["http://u@127.0.0.1:50051", /userinfo is not supported/],
        ["127.0.0.1:abc", /the port "abc" is not a number/],
        ["127.0.0.1:70000", /the port 70000 is out of range/],
        ["ftp://host:1", /unsupported scheme "ftp"/],
        ["dns:///spicedb:50051", /unsupported scheme "dns"/],
        ["http://", /no host/],
        ["", /no host/],
    ] as const)("rejects %s at construction, naming the problem", (endpoint, problem) => {
        let error: unknown;
        try {
            construct(endpoint);
        }
        catch (e) {
            error = e;
        }
        expect(error).toBeInstanceOf(InvalidArgumentError);
        expect((error as Error).message).toMatch(problem);
        expect((error as Error).message).toMatch(FORMS);
    });
    it.each(["http://example.com:50051/x", "http://u@example.com:50051", "ftp://example.com:1"])("reports malformed remote %s as malformed, not as a remote-plaintext refusal", (endpoint) => {
        expect(() => construct(endpoint)).toThrowError(/invalid endpoint/);
        expect(() => construct(endpoint)).not.toThrowError(/remoteCredentials/);
    });
    it("applies the remote-plaintext policy to a well-formed remote http:// URL", () => {
        expect(() => construct("http://example.com:50051")).toThrowError(/non-loopback endpoint "http:\/\/example\.com:50051".*RemoteCredentials\.AllowPlaintext/);
    });
    it("names every option a Docker Compose service endpoint needs", () => {
        expect(() => construct("spicedb:50051")).toThrowError(new InvalidArgumentError(
            'spicedb: refusing to send credentials over plaintext to non-loopback endpoint "spicedb:50051": ' +
            "use TLS (the default), or, for a remote plaintext server such as a Docker Compose service, " +
            "pass both transportSecurity: TransportSecurity.Plaintext and remoteCredentials: RemoteCredentials.AllowPlaintext"));
        expect(() => construct("spicedb:50051", { ...plaintext, remoteCredentials: RemoteCredentials.AllowPlaintext })).not.toThrow();
        expect(() => construct("http://example.com:50051", { ...plaintext, remoteCredentials: RemoteCredentials.AllowPlaintext })).not.toThrow();
    });
    it("shows the endpoint and root cause for a refused connection", async () => {
        const client = createSpiceDBClient("127.0.0.1:1", "test-token", { ...plaintext, retries: Retries.Disabled });
        try {
            const error = await client.readSchema({ timeoutMs: 5000 }).then(() => undefined, (e: unknown) => e);
            expect(error).toBeInstanceOf(UnavailableError);
            expect((error as Error).message).toMatch(/ECONNREFUSED 127\.0\.0\.1:1/);
        }
        finally {
            client.close();
        }
    });
});
