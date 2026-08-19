/**
 * Example: Connecting to a SpiceDB fronted by a private CA (and mutual TLS)
 *
 * By default the client trusts Node's bundled Mozilla root store -- NOT the
 * host's certificate store. So a SpiceDB terminated behind a corporate or
 * cluster-internal CA is unreachable until you hand the client that CA:
 *
 *   const client = createSpiceDBClient("spicedb.internal:443", token, {
 *     tls: { caCert: readFileSync("/etc/ssl/certs/internal-ca.pem") },
 *   });
 *
 * Add `clientCert` and `clientKey` where the server requires mutual TLS --
 * both halves together; either alone is refused.
 *
 * Unlike the other examples here, this one does not use the shared SpiceDB at
 * localhost:50051: a plaintext server has nothing to demonstrate about trust
 * material. It brings up its own TLS-terminated gRPC endpoint signed by a
 * throwaway CA instead, so what runs below is a real handshake against a
 * certificate no public root set would accept.
 */
import { execFileSync } from "node:child_process";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import * as http2 from "node:http2";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { connectNodeAdapter } from "@connectrpc/connect-node";
import { PermissionsService } from "@spicedb/proto";
import { createSpiceDBClient, full } from "../../src/index.js";

function assert(condition: boolean, message: string): void {
  if (!condition) {
    console.error(`ASSERTION FAILED: ${message}`);
    process.exit(1);
  }
}

// --- A throwaway PKI, standing in for whatever your deployment actually uses.
// In production these are files on disk or keys in a mounted secret; the only
// thing the client needs is their PEM contents.
const dir = mkdtempSync(join(tmpdir(), "spicedb-example-tls-"));
const openssl = (...args: string[]) =>
  execFileSync("openssl", args, { stdio: "pipe" });

openssl(
  "req", "-x509", "-newkey", "rsa:2048", "-nodes",
  "-keyout", join(dir, "ca.key"), "-out", join(dir, "ca.crt"),
  "-days", "1", "-subj", "/CN=example internal CA",
  "-addext", "basicConstraints=critical,CA:TRUE",
);

function leaf(name: string, ext: string) {
  writeFileSync(
    join(dir, `${name}.ext`),
    `basicConstraints=CA:FALSE\nkeyUsage=digitalSignature,keyEncipherment\n${ext}\n`,
  );
  openssl(
    "req", "-newkey", "rsa:2048", "-nodes",
    "-keyout", join(dir, `${name}.key`), "-out", join(dir, `${name}.csr`),
    "-subj", `/CN=${name}`,
  );
  openssl(
    "x509", "-req", "-in", join(dir, `${name}.csr`),
    "-CA", join(dir, "ca.crt"), "-CAkey", join(dir, "ca.key"),
    "-CAcreateserial", "-out", join(dir, `${name}.crt`),
    "-days", "1", "-extfile", join(dir, `${name}.ext`),
  );
  return {
    cert: readFileSync(join(dir, `${name}.crt`)),
    key: readFileSync(join(dir, `${name}.key`)),
  };
}

// SAN, not CN: Node's TLS stack ignores the common name entirely.
const server = leaf(
  "localhost",
  "subjectAltName=DNS:localhost,IP:127.0.0.1\nextendedKeyUsage=serverAuth",
);
const clientId = leaf("client", "extendedKeyUsage=clientAuth");
const caCert = readFileSync(join(dir, "ca.crt"));

// One real check. `checkPermissions` with zero checks sends no request at
// all -- an empty bulk check is a round trip whose only possible answer is
// `[]` -- and a call that never reaches the wire proves nothing about the
// handshake this example exists to demonstrate.
const ONE_CHECK = {
  resourceType: "document",
  resourceId: "readme",
  permission: "view",
  subjectType: "user",
  subjectId: "alice",
};

// --- A stand-in for a TLS-terminated SpiceDB.
function serve(requireClientCert: boolean): Promise<{
  port: number;
  close: () => Promise<void>;
}> {
  const handler = connectNodeAdapter({
    routes: (router) => {
      router.service(PermissionsService, {
        // One pair per request item: the client rejects a response whose
        // pair count does not match the request's item count.
        checkBulkPermissions: (req: { items: unknown[] }) => ({
          pairs: req.items.map(() => ({
            response: { case: "item" as const, value: {} },
          })),
        }),
      });
    },
  });
  const httpServer = http2.createSecureServer(
    {
      key: server.key,
      cert: server.cert,
      ...(requireClientCert
        ? { ca: caCert, requestCert: true, rejectUnauthorized: true }
        : {}),
    },
    handler,
  );
  return new Promise((resolve) => {
    httpServer.listen(0, "localhost", () => {
      const address = httpServer.address();
      if (address === null || typeof address === "string") {
        throw new Error("expected a TCP address from the example server");
      }
      resolve({
        port: address.port,
        close: () =>
          new Promise<void>((done) => httpServer.close(() => done())),
      });
    });
  });
}

try {
  // --- 1. A private CA.
  {
    const { port, close } = await serve(false);
    const client = createSpiceDBClient(`localhost:${port}`, "testtoken", {
      tls: { caCert },
    });
    const results = await client.checkPermissions(full(), ONE_CHECK);
    console.log(
      `connected over TLS to a private CA; ${results.length} check result(s)`,
    );
    assert(results.length === 1, "the call must complete over TLS");
    client.close();
    await close();
  }

  // --- 2. Mutual TLS: the server demands the client's own certificate too.
  {
    const { port, close } = await serve(true);
    const client = createSpiceDBClient(`localhost:${port}`, "testtoken", {
      tls: {
        caCert,
        clientCert: clientId.cert,
        clientKey: clientId.key,
      },
    });
    const results = await client.checkPermissions(full(), ONE_CHECK);
    console.log(
      `connected over mutual TLS; ${results.length} check result(s)`,
    );
    assert(results.length === 1, "the mutual-TLS call must complete");
    client.close();
    await close();
  }

  // --- 3. Trust material never makes the connection plaintext.
  //
  // A plaintext connection performs no TLS handshake, so node:tls would drop
  // `caCert` and the bearer token would go out in cleartext behind a call site
  // that reads as though TLS were configured. See root DESIGN.md, "RULE:
  // Credentials over insecure transport require an explicit opt-in".
  {
    let refused = false;
    try {
      createSpiceDBClient("localhost:50051", "testtoken", {
        insecure: true,
        tls: { caCert },
      });
    } catch {
      refused = true;
    }
    assert(refused, "insecure + tls must be refused, not silently ignored");
    console.log("insecure + tls is refused at construction, as it should be");
  }

  console.log("custom TLS example completed");
} finally {
  rmSync(dir, { recursive: true, force: true });
}
