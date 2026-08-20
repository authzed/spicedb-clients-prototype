/**
 * Example: both directions of "a conversion that cannot preserve meaning must fail"
 *
 * Root DESIGN.md, "RULE: A conversion that cannot preserve meaning must fail".
 *
 * The rule has two clauses that point opposite ways, and confusing them is the
 * failure mode either way:
 *
 * 1. Data the CALLER supplied that the client cannot represent must raise a
 *    typed error naming what could not be converted. The caller can see the
 *    failure and fix their input, so the client neither approximates the value
 *    nor drops it -- silently discarding it turns a caller's mistake into a
 *    silent wrong answer.
 * 2. Values the SERVER supplied that the client does not recognise must NOT
 *    raise, and must map to the safe, non-permissive default -- never a grant.
 *    Raising would turn a routine SpiceDB upgrade that adds an enum value into
 *    a client-side outage.
 *
 * The last part covers clause 2 and needs a server that emits a permissionship
 * this client has never heard of, so it stands up a stand-in.
 */
import * as http2 from "node:http2";
import { connectNodeAdapter } from "@connectrpc/connect-node";
import { PermissionsService } from "@spicedb/proto";
import {
  createSpiceDBClient,
  full,
  InvalidArgumentError,
} from "../../src/index.js";

const ENDPOINT = process.env.SPICEDB_ENDPOINT ?? "localhost:50051";
const TOKEN = process.env.SPICEDB_TOKEN ?? "somerandomkeyhere";

function assert(condition: unknown, message: string): asserts condition {
  if (!condition) {
    throw new Error(message);
  }
}

async function main(): Promise<void> {
  // ── 1. Caller data: a filter the wire format cannot express ──────────
  //
  // A subject ID with no subject type is not a narrower filter -- the wire
  // format simply drops it, so the filter silently WIDENS. Applied to
  // deleteRelationships that is the difference between deleting alice's
  // relationships and deleting every relationship on every document.
  const live = createSpiceDBClient(ENDPOINT, TOKEN, { insecure: true });
  let threw = false;
  try {
    // The filter is rejected before a request is built, so nothing reaches the
    // server: this is the caller-facing surface, not an internal helper.
    for await (const _ of live.readRelationships(
      { resourceType: "document", subjectId: "alice" },
      full(),
    )) {
      // unreachable
    }
  } catch (err) {
    assert(
      err instanceof InvalidArgumentError,
      `the failure must be typed, got ${String(err)}`,
    );
    assert(
      err.message.includes("subjectType"),
      `the failure must name the missing piece, got: ${err.message}`,
    );
    threw = true;
  }
  assert(
    threw,
    "a filter whose subject constraint the wire cannot express must fail, not widen",
  );
  console.log("subject ID without subject type: refused rather than widened");

  // The same filter with the missing piece supplied converts fine, which is
  // what makes the check above a real constraint rather than a blanket ban.
  for await (const _ of live.readRelationships(
    { resourceType: "document", subjectType: "user", subjectId: "alice" },
    full(),
  )) {
    // A fully-specified subject filter converts and streams; whether it matches
    // anything is beside the point.
  }
  console.log("...and converts once subjectType is supplied");

  // ── 2. Server data: an enum this client has never seen ───────────────
  //
  // The opposite posture. This must not throw, and must not be a grant.
  const handler = connectNodeAdapter({
    routes: (router) => {
      router.service(PermissionsService, {
        // This client's checkPermission calls the UNARY CheckPermission RPC,
        // not CheckBulkPermissions as the Go and Python clients do -- a
        // difference worth knowing when writing a stand-in for it.
        //
        // 4242 is not a value this client's enum knows. A SpiceDB that added a
        // permissionship after this client shipped would look exactly like this
        // on the wire.
        checkPermission: () => ({ permissionship: 4242 as never }),
      });
    },
  });
  const server = http2.createServer(handler);
  await new Promise<void>((resolve) =>
    server.listen(0, "localhost", () => resolve()),
  );
  const address = server.address();
  assert(
    address !== null && typeof address !== "string",
    "expected a TCP address from the stand-in",
  );

  const client = createSpiceDBClient(`localhost:${address.port}`, "some-token", {
    insecure: true,
  });
  try {
    const result = await client.checkPermission(full(), {
      resourceType: "document",
      resourceId: "readme",
      permission: "view",
      subjectType: "user",
      subjectId: "alice",
    });
    assert(
      !result.hasPermission(),
      "SECURITY: an unrecognised permissionship was treated as a grant",
    );
    console.log("unknown server permissionship: no error, and not a grant");
  } finally {
    // Close the clients BEFORE the server: http2's close() waits for existing
    // sessions to end, so a still-open client connection means its callback
    // never fires and the example hangs -- which CI cannot distinguish from an
    // example that is merely slow.
    client.close();
    live.close();
    await new Promise<void>((resolve) => server.close(() => resolve()));
  }

  console.log(
    "unrepresentable_values: caller data fails loudly, server data degrades safely",
  );
}

main().catch((err: unknown) => {
  console.error(err);
  process.exit(1);
});
