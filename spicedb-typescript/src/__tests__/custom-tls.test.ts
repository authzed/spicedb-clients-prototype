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
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import * as http2 from "node:http2";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { connectNodeAdapter } from "@connectrpc/connect-node";
import { PermissionsService } from "@spicedb/proto";
import { SpiceDBClient } from "../client.js";
import { full } from "../consistency.js";

const TOKEN = "test-token";

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
  openssl(
    "req", "-x509", "-newkey", "rsa:2048", "-nodes",
    "-keyout", join(dir, "ca.key"), "-out", join(dir, "ca.crt"),
    "-days", "1", "-subj", "/CN=spicedb-typescript test CA",
    "-addext", "basicConstraints=critical,CA:TRUE",
  );
  // SAN, not CN: Node's TLS stack ignores the common name entirely.
  writeFileSync(
    join(dir, "server.ext"),
    "basicConstraints=CA:FALSE\nkeyUsage=digitalSignature,keyEncipherment\n" +
      "subjectAltName=DNS:localhost,IP:127.0.0.1\nextendedKeyUsage=serverAuth\n",
  );
  openssl(
    "req", "-newkey", "rsa:2048", "-nodes",
    "-keyout", join(dir, "server.key"), "-out", join(dir, "server.csr"),
    "-subj", "/CN=localhost",
  );
  openssl(
    "x509", "-req", "-in", join(dir, "server.csr"),
    "-CA", join(dir, "ca.crt"), "-CAkey", join(dir, "ca.key"),
    "-CAcreateserial", "-out", join(dir, "server.crt"),
    "-days", "1", "-extfile", join(dir, "server.ext"),
  );
  ca = readFileSync(join(dir, "ca.crt"));
  serverCert = readFileSync(join(dir, "server.crt"));
  serverKey = readFileSync(join(dir, "server.key"));
}, 60_000);

afterAll(() => {
  rmSync(dir, { recursive: true, force: true });
});

async function serve(): Promise<{ port: number; close: () => Promise<void> }> {
  const handler = connectNodeAdapter({
    routes: (router) => {
      router.service(PermissionsService, {
        checkBulkPermissions: () => ({ pairs: [] }),
      });
    },
  });
  const server = http2.createSecureServer(
    { key: serverKey, cert: serverCert },
    handler,
  );
  await new Promise<void>((resolve) => server.listen(0, "localhost", resolve));
  const address = server.address();
  if (address === null || typeof address === "string") {
    throw new Error("expected a TCP address from the test server");
  }
  return {
    port: address.port,
    close: () => new Promise<void>((resolve) => server.close(() => resolve())),
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
        maxRetries: 0,
      });
      await expect(trusting.checkPermissions(full())).resolves.toEqual([]);
      trusting.close();

      // Same server, same call, no `tls`: Node's bundled roots have never
      // heard of the CA above -- exactly the position an operator behind a
      // private CA is in, and the reason the option had to exist.
      const untrusting = new SpiceDBClient({
        endpoint: `localhost:${port}`,
        token: TOKEN,
        maxRetries: 0,
      });
      await expect(untrusting.checkPermissions(full())).rejects.toThrow();
      untrusting.close();
    } finally {
      await close();
    }
  }, 30_000);

  it("refuses trust material combined with insecure, at construction", () => {
    // Supplying a CA must not become a quieter route to a plaintext channel:
    // node:tls would drop it silently and the bearer token would go out in
    // cleartext behind a call site that reads as though TLS were configured.
    // Root DESIGN.md, "RULE: Credentials over insecure transport require an
    // explicit opt-in".
    expect(
      () =>
        new SpiceDBClient({
          endpoint: "localhost:50051",
          token: TOKEN,
          insecure: true,
          tls: { caCert: "pem" },
        }),
    ).toThrow(/insecure: true and TLS material/);
  });

  it("keeps the non-loopback refusal ahead of the TLS one", () => {
    expect(
      () =>
        new SpiceDBClient({
          endpoint: "evil.example.com:443",
          token: TOKEN,
          insecure: true,
          tls: { caCert: "pem" },
        }),
    ).toThrow(/allowInsecureRemoteCredentials/);
  });
});
