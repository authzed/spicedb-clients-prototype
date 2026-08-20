/**
 * The escape hatch reaches a real stub and makes a real call.
 *
 * `raw()` exists so a request the idiomatic surface cannot express has a
 * workaround short of forking the client -- root DESIGN.md, "What NOT To Do",
 * permits exactly this as "clearly marked secondary API". Asserting that the
 * accessor exists, or that it returns something with a `permissions` property,
 * would prove none of that. What matters is whether a caller can drive a
 * generated Connect client through it and get an answer out of a real server,
 * with this client's bearer token attached.
 *
 * So these tests run a real plaintext HTTP/2 (h2c) server behind
 * `connectNodeAdapter`, and check both the response and the `authorization`
 * header the server actually received. No TLS, so unlike custom-tls.test.ts
 * these need no `openssl`.
 */
import { describe, it, expect } from "vitest";
import * as http2 from "node:http2";
import { connectNodeAdapter } from "@connectrpc/connect-node";
import { PermissionsService } from "@spicedb/proto";
import { SpiceDBClient } from "../client.js";
import { full } from "../consistency.js";

const TOKEN = "test-token";

/**
 * Serves `CheckBulkPermissions` for real over h2c, recording the
 * `authorization` header of every request that arrives.
 */
async function serve(): Promise<{
  port: number;
  authorizations: string[];
  close: () => Promise<void>;
}> {
  const authorizations: string[] = [];
  const handler = connectNodeAdapter({
    routes: (router) => {
      router.service(PermissionsService, {
        checkBulkPermissions: (req, ctx) => {
          authorizations.push(ctx.requestHeader.get("authorization") ?? "");
          return {
            pairs: req.items.map(() => ({
              response: {
                case: "item" as const,
                value: { permissionship: 2 }, // HAS_PERMISSION
              },
            })),
          };
        },
      });
    },
  });
  const server = http2.createServer(handler);
  await new Promise<void>((resolve) => server.listen(0, "localhost", resolve));
  const address = server.address();
  if (address === null || typeof address === "string") {
    throw new Error("expected a TCP address from the test server");
  }
  return {
    port: address.port,
    authorizations,
    close: () => new Promise<void>((resolve) => server.close(() => resolve())),
  };
}

function bulkRequest() {
  return {
    consistency: {
      requirement: { case: "fullyConsistent" as const, value: true },
    },
    items: [
      {
        resource: { objectType: "document", objectId: "readme" },
        permission: "view",
        subject: { object: { objectType: "user", objectId: "jimmy" } },
      },
    ],
  };
}

describe("SpiceDBClient raw()", () => {
  it("drives a real generated client against a real server", async () => {
    const { port, authorizations, close } = await serve();
    const client = new SpiceDBClient({
      endpoint: `localhost:${port}`,
      token: TOKEN,
      insecure: true,
      maxRetries: 0,
    });
    try {
      const response =
        await client.raw().permissions.checkBulkPermissions(bulkRequest());

      expect(response.pairs).toHaveLength(1);
      expect(response.pairs[0].response.case).toBe("item");
      // The bearer token rides the transport interceptor, so a raw call is
      // authenticated exactly as an idiomatic one is.
      expect(authorizations).toEqual([`Bearer ${TOKEN}`]);
    } finally {
      client.close();
      await close();
    }
  }, 30_000);

  it("hands back the very client the idiomatic methods call through", async () => {
    const { port, authorizations, close } = await serve();
    const client = new SpiceDBClient({
      endpoint: `localhost:${port}`,
      token: TOKEN,
      insecure: true,
      maxRetries: 0,
    });
    try {
      // Not a second connection built behind the caller's back: the same
      // object every time, and the same one an idiomatic call uses.
      expect(client.raw()).toBe(client.raw());

      const idiomatic = await client.checkPermissions(full(), {
        resourceType: "document",
        resourceId: "readme",
        permission: "view",
        subjectType: "user",
        subjectId: "jimmy",
      });
      expect(idiomatic).toHaveLength(1);

      await client.raw().permissions.checkBulkPermissions(bulkRequest());

      // One idiomatic call and one raw call, both authenticated, both over the
      // connection this client owns.
      expect(authorizations).toEqual([`Bearer ${TOKEN}`, `Bearer ${TOKEN}`]);
    } finally {
      client.close();
      await close();
    }
  }, 30_000);

  it("is an accessor, not a second construction path", () => {
    // Root DESIGN.md, "RULE: Credentials over insecure transport require an
    // explicit opt-in", is enforced in the proto tier's createSpiceDBClient,
    // on the single path that builds a transport. Handing back an
    // already-built client cannot bypass that; accepting an endpoint, token,
    // or transport setting here would -- it would be a second construction
    // path with no guard on it. Pin the shape that makes that impossible.
    expect(SpiceDBClient.prototype.raw.length).toBe(0);

    // And the guard still refuses the combination it always did, so the hatch
    // has not moved construction anywhere.
    expect(
      () =>
        new SpiceDBClient({
          endpoint: "evil.example.com:50051",
          token: TOKEN,
          insecure: true,
        }),
    ).toThrow(/allowInsecureRemoteCredentials/);
  });
});
