/**
 * Example: Resource lookup
 *
 * Demonstrates looking up all resources a subject has access to, including
 * how to read the `permissionship` of each result. A permissionship of
 * `"conditionalPermission"` means the match depends on caveat context that
 * wasn't supplied — callers must not treat it as an unconditional grant.
 *
 * Also demonstrates `params.debug`, which asks the server for extra context
 * on a `MAXIMUM_DEPTH_EXCEEDED` failure. That failure needs a schema deep
 * enough to overrun the server's recursion limit, which isn't practical to
 * provoke here -- so that part stands up its own stand-in server (same
 * technique as `examples/error_mapping/`) that always fails that way, purely
 * to prove the flag reaches the request and the server's extra detail
 * survives into `reasonMetadata`.
 */
import * as http2 from "node:http2";
import { Code, ConnectError } from "@connectrpc/connect";
import { connectNodeAdapter } from "@connectrpc/connect-node";
import { PermissionsService, ErrorInfoSchema } from "@spicedb/proto";
import {
  createSpiceDBClient,
  Transaction,
  relationship,
  full,
  ResourceExhaustedError,
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

// Setup: write schema and data
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

const txn = new Transaction();
txn.touch(relationship("document:readme", "viewer", "user:jimmy"));
txn.touch(relationship("document:design", "editor", "user:jimmy"));
await client.write(txn);

// Find all documents that user:jimmy can view
console.log("Documents user:jimmy can view:");
const found = new Set<string>();
for await (const resource of client.lookupResources(
  {
    resourceType: "document",
    permission: "view",
    subjectType: "user",
    subjectId: "jimmy",
  },
  full(),
)) {
  console.log(
    `  document:${resource.resourceId} (permissionship=${resource.permissionship})`,
  );
  if (resource.permissionship !== "hasPermission") {
    // A conditional result means caveat context is missing; partialCaveat
    // lists which context. Never treat a conditional result as a full
    // grant.
    console.error(
      `ASSERTION FAILED: unexpected permissionship for document:${resource.resourceId}: ` +
        `${resource.permissionship} (missing context: ${JSON.stringify(resource.partialCaveat)})`,
    );
    process.exit(1);
  }
  // lookedUpAt is the revision this result was computed at — thread it into
  // atLeast()/atLeastOrFull() for read-your-writes on a later call.
  assert(
    resource.lookedUpAt !== "",
    `expected a non-empty lookedUpAt token for document:${resource.resourceId}`,
  );
  found.add(resource.resourceId);
}

assert(found.has("readme"), "expected readme in results");
assert(found.has("design"), "expected design in results (editor implies view)");

// Clean up so later examples that write a narrower schema aren't blocked by
// leftover relationships (examples run in sequence against one shared
// SpiceDB instance).
await client.deleteRelationships({ resourceType: "document" });

// Release the underlying transport now that this example is done with it.
client.close();

// ── params.debug: extra context on a MAXIMUM_DEPTH_EXCEEDED failure ──────
//
// The stand-in always fails the same way real SpiceDB does when the
// recursion limit is hit: RESOURCE_EXHAUSTED with an ErrorInfo detail whose
// reason is ERROR_REASON_MAXIMUM_DEPTH_EXCEEDED and a `maximum_depth_allowed`
// metadata key. It echoes `req.withDebug` back as a `debug_requested`
// metadata key too, standing in for whatever extra content a real server
// attaches -- the content is server-defined, but this client's contract is
// simply that whatever comes back surfaces in `reasonMetadata`.
function maxDepthServer(): http2.Http2Server {
  const handler = connectNodeAdapter({
    routes: (router) => {
      router.service(PermissionsService, {
        lookupResources: async function* (req: { withDebug: boolean }) {
          throw new ConnectError(
            "max recursion depth exceeded",
            Code.ResourceExhausted,
            undefined,
            [
              {
                desc: ErrorInfoSchema,
                value: {
                  reason: "ERROR_REASON_MAXIMUM_DEPTH_EXCEEDED",
                  domain: "authzed.com",
                  metadata: {
                    maximum_depth_allowed: "50",
                    debug_requested: String(req.withDebug),
                  },
                },
              },
            ],
          );
        },
      });
    },
  });
  return http2.createServer(handler);
}

async function listen(server: http2.Http2Server): Promise<number> {
  await new Promise<void>((resolve) =>
    server.listen(0, "localhost", () => resolve()),
  );
  const address = server.address();
  if (address === null || typeof address === "string") {
    throw new Error("expected a TCP address from the stand-in");
  }
  return address.port;
}

const debugServer = maxDepthServer();
const debugPort = await listen(debugServer);
const debugClient = createSpiceDBClient(`localhost:${debugPort}`, "testtoken", {
  insecure: true,
});
try {
  for (const debug of [false, true]) {
    let caught: unknown;
    try {
      for await (const _resource of debugClient.lookupResources(
        {
          resourceType: "document",
          permission: "view",
          subjectType: "user",
          subjectId: "jimmy",
          debug,
        },
        full(),
      )) {
        // unreachable -- the stand-in always throws before yielding
      }
    } catch (err) {
      caught = err;
    }
    if (!(caught instanceof ResourceExhaustedError)) {
      console.error(
        `ASSERTION FAILED: expected ResourceExhaustedError, got: ${String(caught)}`,
      );
      process.exit(1);
    }
    assert(
      caught.reason === "ERROR_REASON_MAXIMUM_DEPTH_EXCEEDED",
      `expected the MAXIMUM_DEPTH_EXCEEDED reason, got: ${caught.reason}`,
    );
    assert(
      caught.reasonMetadata.debug_requested === String(debug),
      `expected debug_requested=${debug} to round-trip through the request, got: ${caught.reasonMetadata.debug_requested}`,
    );
  }
  console.log(
    "lookupResources debug flag: round-trips through the request and the server's extra detail lands in reasonMetadata",
  );
} finally {
  debugClient.close();
  await new Promise<void>((resolve) => debugServer.close(() => resolve()));
}

console.log("lookup_resources: PASS");
