/**
 * Caller-supplied TLS trust material, proven against a real TLS handshake.
 *
 * Root DESIGN.md, "RULE: A system-TLS constructor must reach a real server",
 * lets this client delegate to the runtime's default trust source *because* a
 * caller can supply their own material when that default is not enough -- and
 * it names the hazard that makes the escape hatch necessary: Node ships a
 * bundled Mozilla root store, so a CA an operator installed in the host's own
 * store is not honoured here. Until `tls.caCert` existed, a SpiceDB behind a
 * private or corporate CA was simply unreachable, and that justification was
 * not true.
 *
 * Every connection test below stands up a real HTTP/2 server over TLS with a
 * certificate signed by a throwaway CA generated in-process, serves the real
 * PermissionsService on it, and drives a real client against it. The pairing
 * is what makes each assertion mean something: same server, same client code,
 * differing only in whether the CA (or the client identity) was supplied. A
 * test that only asserted the failure could not tell a rejected certificate
 * from an unreachable port; one that only asserted the success could not tell
 * a verified chain from a disabled one.
 */
import { describe, it, expect, beforeAll, afterAll, vi } from "vitest";
import { execFileSync } from "node:child_process";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import * as http2 from "node:http2";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { connectNodeAdapter, Http2SessionManager } from "@connectrpc/connect-node";
import { createSpiceDBClient } from "../client.js";
import { PermissionsService } from "../gen/authzed/api/v1/permission_service_pb.js";

const TOKEN = "test-token";

interface Pki {
  ca: Buffer;
  serverCert: Buffer;
  serverKey: Buffer;
  clientCert: Buffer;
  clientKey: Buffer;
}

let dir: string;
let pki: Pki;

function openssl(...args: string[]): void {
  // Not spawned through a shell, and it throws rather than skipping if
  // `openssl` is missing: a skipped test reads as coverage while proving
  // nothing, which is the failure mode the rule above exists to catch.
  execFileSync("openssl", args, { stdio: "pipe" });
}

function leaf(name: string, ext: string): { cert: Buffer; key: Buffer } {
  writeFileSync(
    join(dir, `${name}.ext`),
    `basicConstraints=CA:FALSE\nkeyUsage=digitalSignature,keyEncipherment\n${ext}\n`,
  );
  openssl(
    "req", "-newkey", "rsa:2048", "-nodes",
    "-keyout", join(dir, `${name}.key`),
    "-out", join(dir, `${name}.csr`),
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

beforeAll(() => {
  // Generated rather than committed: a checked-in certificate expires, and an
  // expiry is a test that starts failing for a reason unrelated to the code
  // it covers.
  dir = mkdtempSync(join(tmpdir(), "spicedb-tls-"));
  openssl(
    "req", "-x509", "-newkey", "rsa:2048", "-nodes",
    "-keyout", join(dir, "ca.key"), "-out", join(dir, "ca.crt"),
    "-days", "1", "-subj", "/CN=spicedb-typescript-proto test CA",
    "-addext", "basicConstraints=critical,CA:TRUE",
  );
  // SAN, not CN: Node's TLS stack ignores the common name entirely, so a
  // certificate without a matching SAN fails verification even against its
  // own CA.
  const server = leaf(
    "localhost",
    "subjectAltName=DNS:localhost,IP:127.0.0.1\nextendedKeyUsage=serverAuth",
  );
  const client = leaf("client", "extendedKeyUsage=clientAuth");
  pki = {
    ca: readFileSync(join(dir, "ca.crt")),
    serverCert: server.cert,
    serverKey: server.key,
    clientCert: client.cert,
    clientKey: client.key,
  };
}, 60_000);

afterAll(() => {
  rmSync(dir, { recursive: true, force: true });
});

/**
 * A real gRPC-over-TLS endpoint serving the real PermissionsService, so a
 * completed call proves the whole path -- handshake, HTTP/2, gRPC framing --
 * and not just a socket that opened.
 */
async function serve(
  requestCert: boolean,
): Promise<{ port: number; close: () => Promise<void> }> {
  const handler = connectNodeAdapter({
    routes: (router) => {
      router.service(PermissionsService, {
        checkBulkPermissions: () => ({ pairs: [] }),
      });
    },
  });
  const server = http2.createSecureServer(
    {
      key: pki.serverKey,
      cert: pki.serverCert,
      ...(requestCert
        ? { ca: pki.ca, requestCert: true, rejectUnauthorized: true }
        : {}),
    },
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

describe("caller-supplied TLS trust material", () => {
  it("reaches a server whose certificate the default roots reject", async () => {
    const { port, close } = await serve(false);
    try {
      const trusting = createSpiceDBClient(`localhost:${port}`, TOKEN, {
        tls: { caCert: pki.ca },
      });
      await expect(
        trusting.permissions.checkBulkPermissions({ items: [] }),
      ).resolves.toMatchObject({ pairs: [] });
      trusting.close();

      // Same server, same call, no CA: Node's bundled roots have never heard
      // of the CA generated above, which is exactly the position an operator
      // behind a private CA is in.
      const untrusting = createSpiceDBClient(`localhost:${port}`, TOKEN);
      await expect(
        untrusting.permissions.checkBulkPermissions({ items: [] }),
      ).rejects.toThrow();
      untrusting.close();
    } finally {
      await close();
    }
  }, 30_000);

  it("presents a client identity to a server requiring mutual TLS", async () => {
    const { port, close } = await serve(true);
    try {
      const identified = createSpiceDBClient(`localhost:${port}`, TOKEN, {
        tls: {
          caCert: pki.ca,
          clientCert: pki.clientCert,
          clientKey: pki.clientKey,
        },
      });
      await expect(
        identified.permissions.checkBulkPermissions({ items: [] }),
      ).resolves.toMatchObject({ pairs: [] });
      identified.close();

      // Identical `caCert`, so this can only fail for the missing client
      // identity -- the server refuses any connection that does not present a
      // certificate under its CA.
      const anonymous = createSpiceDBClient(`localhost:${port}`, TOKEN, {
        tls: { caCert: pki.ca },
      });
      await expect(
        anonymous.permissions.checkBulkPermissions({ items: [] }),
      ).rejects.toThrow();
      anonymous.close();
    } finally {
      await close();
    }
  }, 30_000);

  it("accepts the PEM material as a string, not only a Buffer", async () => {
    // The options are typed as whatever `node:tls` accepts for ca/cert/key,
    // which includes plain strings -- the shape a caller who read the file
    // with an encoding, or pulled the PEM out of an environment variable,
    // actually has in hand.
    const { port, close } = await serve(false);
    try {
      const client = createSpiceDBClient(`localhost:${port}`, TOKEN, {
        tls: { caCert: pki.ca.toString("utf8") },
      });
      await expect(
        client.permissions.checkBulkPermissions({ items: [] }),
      ).resolves.toMatchObject({ pairs: [] });
      client.close();
    } finally {
      await close();
    }
  }, 30_000);
});

describe("TLS material is not a way around the credential guard", () => {
  // Root DESIGN.md, "RULE: Credentials over insecure transport require an
  // explicit opt-in". node:tls would silently drop ca/cert/key on a plaintext
  // socket, so a call site reading `{ insecure: true, tls: { caCert } }` would
  // ship the bearer token in cleartext while looking like it configured TLS.
  it.each([
    ["caCert", { caCert: "pem" }],
    ["client identity", { clientCert: "pem", clientKey: "pem" }],
  ])("refuses insecure together with %s rather than ignoring it", (_name, tls) => {
    expect(() =>
      createSpiceDBClient("localhost:50051", TOKEN, { insecure: true, tls }),
    ).toThrow(/insecure: true and TLS material/);
  });

  it("still applies the non-loopback refusal first, with trust material in hand", () => {
    // Supplying a CA must not become a second constructor that skips the
    // guard -- and the credential-leak message, not the TLS one, is what the
    // caller sees.
    expect(() =>
      createSpiceDBClient("evil.example.com:443", TOKEN, {
        insecure: true,
        tls: { caCert: "pem" },
      }),
    ).toThrow(/allowInsecureRemoteCredentials/);
  });

  it("refuses to build the client before any connection is opened", () => {
    // The refusal must land in createSpiceDBClient, not on the first call: a
    // client that accepted the combination and only failed later would
    // already be a token leak waiting for its first RPC.
    const spy = vi.spyOn(Http2SessionManager.prototype, "connect");
    try {
      expect(() =>
        createSpiceDBClient("localhost:50051", TOKEN, {
          insecure: true,
          tls: { caCert: "pem" },
        }),
      ).toThrow();
      expect(spy).not.toHaveBeenCalled();
    } finally {
      spy.mockRestore();
    }
  });
});

describe("a half client identity", () => {
  it.each([
    ["clientCert without clientKey", { clientCert: "pem" }, /clientKey/],
    ["clientKey without clientCert", { clientKey: "pem" }, /clientCert/],
  ])("refuses %s", (_name, tls, missing) => {
    expect(() =>
      createSpiceDBClient("spicedb.example.com:443", TOKEN, { tls }),
    ).toThrow(missing);
  });
});
