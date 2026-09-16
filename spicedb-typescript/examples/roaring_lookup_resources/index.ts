/**
 * Example: experimentalRoaringLookupResources
 *
 * Demonstrates the experimental `RoaringLookupResources` API: it returns a
 * roaring64 bitmap of the resource object IDs (44-bit canonical decimal
 * integers) a subject has a permission on, meant for bulk export into a
 * search index that supports roaring-bitmap terms queries -- see this
 * client's own doc comment on `experimentalRoaringLookupResources` and the
 * proto's `RoaringLookupResourcesService` comment for the full contract.
 *
 * Why this example stands up its own server
 * ------------------------------------------
 * `RoaringLookupResourcesService` is new, and the SpiceDB image
 * `docker-compose.test.yml` starts does not implement it yet -- calling it
 * there fails with UNIMPLEMENTED ("unknown service
 * authzed.api.materialize.v0.RoaringLookupResourcesService"), verified
 * directly against that image rather than assumed. A stand-in server lets
 * this example exercise the real request/response wire mapping today --
 * including the FAILED_PRECONDITION a non-canonical resource object ID
 * produces -- and it will keep passing once the shared server catches up.
 */
import * as http2 from "node:http2";
import { Code, ConnectError } from "@connectrpc/connect";
import { connectNodeAdapter } from "@connectrpc/connect-node";
import { RoaringLookupResourcesService } from "@spicedb/proto";
import {
  createSpiceDBClient,
  full,
  FailedPreconditionError,
} from "../../src/index.js";

function assert(condition: unknown, message: string): asserts condition {
  if (!condition) {
    throw new Error(message);
  }
}

// Stands in for a real roaring64 payload -- this client does not decode the
// bitmap, so the example only needs to prove the bytes round-trip unchanged.
const BITMAP = new Uint8Array([0x3a, 0x30, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00]);
const CARDINALITY = 2n;
const REVISION_TOKEN = "rev-123";

function standIn(): http2.Http2Server {
  const handler = connectNodeAdapter({
    routes: (router) => {
      router.service(RoaringLookupResourcesService, {
        experimentalRoaringLookupResources: (req) => {
          // A stand-in for the FAILED_PRECONDITION the real server returns
          // when a resource object ID of the requested type is not a
          // canonical decimal integer that fits in 44 bits.
          if (req.resourceObjectType === "non_canonical_ids") {
            throw new ConnectError(
              'resource object id "not-a-number" is not a canonical decimal integer',
              Code.FailedPrecondition,
            );
          }
          return {
            bitmap: BITMAP,
            cardinality: CARDINALITY,
            atRevision: { token: REVISION_TOKEN },
          };
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
  assert(
    address !== null && typeof address !== "string",
    "expected a TCP address from the stand-in",
  );
  return address.port;
}

async function main(): Promise<void> {
  const server = standIn();
  const port = await listen(server);
  const client = createSpiceDBClient(`localhost:${port}`, "some-token", {
    insecure: true,
  });
  try {
    const result = await client.experimentalRoaringLookupResources(
      {
        resourceType: "document",
        permission: "view",
        subjectType: "user",
        subjectId: "jimmy",
      },
      full(),
    );
    assert(
      result.cardinality === CARDINALITY,
      `expected cardinality ${CARDINALITY}, got ${result.cardinality}`,
    );
    assert(
      Buffer.from(result.bitmap).equals(Buffer.from(BITMAP)),
      "bitmap bytes must round-trip unchanged",
    );
    assert(
      result.revision === REVISION_TOKEN,
      `expected revision ${REVISION_TOKEN}, got ${result.revision}`,
    );
    console.log(
      `experimentalRoaringLookupResources: cardinality=${result.cardinality}, ` +
        `revision=${result.revision}, bitmap=${result.bitmap.length} bytes`,
    );

    // A non-canonical resource object ID fails the whole call rather than
    // returning a partial bitmap -- a bitmap that is quietly too small is a
    // wrong authorization answer.
    let caught: unknown;
    try {
      await client.experimentalRoaringLookupResources(
        {
          resourceType: "non_canonical_ids",
          permission: "view",
          subjectType: "user",
          subjectId: "jimmy",
        },
        full(),
      );
    } catch (err) {
      caught = err;
    }
    assert(
      caught instanceof FailedPreconditionError,
      `a non-canonical resource object ID must surface as FailedPreconditionError, not a generic failure: got ${String(caught)}`,
    );
    console.log(
      "non-canonical resource object ID: FailedPreconditionError, not a partial bitmap",
    );
  } finally {
    client.close();
    await new Promise<void>((resolve) => server.close(() => resolve()));
  }

  console.log("roaring_lookup_resources: wire mapping and FAILED_PRECONDITION both verified");
}

main().catch((err: unknown) => {
  console.error(err);
  process.exit(1);
});
