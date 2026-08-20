/**
 * Example: the two error codes a caller actually recovers from
 *
 * Root DESIGN.md, "RULE: Error mapping must not lose the server's detail".
 *
 * The rule names both consequences, and this example is those two recoveries
 * written out as running code:
 *
 * - OUT_OF_RANGE is SpiceDB's signal that a ZedToken has expired or been
 *   garbage-collected. Recovery is mechanical: discard the stale token and
 *   re-read at full consistency. Collapsed into a generic error, every caller
 *   would have to string-match a message to recover something the client
 *   already knew the shape of.
 * - UNAUTHENTICATED is the most common error a new integration produces -- a
 *   wrong, expired or rotated token. Distinguishing it is what lets a caller
 *   write "refresh credentials on auth failure, page someone on internal
 *   error".
 *
 * Why this example stands up its own server
 * -----------------------------------------
 * Neither code is reachable from the SpiceDB the integration job starts, which
 * was verified rather than assumed: a garbage ZedToken returns INVALID_ARGUMENT,
 * and the in-memory datastore never collects the revision (with a 5s GC window
 * and 35s elapsed, a snapshot read at the old token still succeeded). And a
 * wrong preshared key comes back PERMISSION_DENIED, not UNAUTHENTICATED -- which
 * the last part asserts against the real server, so a reader does not write a
 * credential-refresh branch that can never run.
 */
import * as http2 from "node:http2";
import { Code, ConnectError } from "@connectrpc/connect";
import { connectNodeAdapter } from "@connectrpc/connect-node";
import { PermissionsService } from "@spicedb/proto";
import {
  createSpiceDBClient,
  atLeast,
  full,
  OutOfRangeError,
  PermissionDeniedError,
  relationship,
  UnauthenticatedError,
  UnavailableError,
} from "../../src/index.js";

const ENDPOINT = process.env.SPICEDB_ENDPOINT ?? "localhost:50051";
const STALE_TOKEN = "stale-zedtoken";
const DOC = {
  resourceType: "document",
  resourceId: "readme",
  permission: "view",
  subjectType: "user",
  subjectId: "alice",
};

function assert(condition: unknown, message: string): asserts condition {
  if (!condition) {
    throw new Error(message);
  }
}

function standIn(): http2.Http2Server {
  const handler = connectNodeAdapter({
    routes: (router) => {
      router.service(PermissionsService, {
        checkPermission: (req) => {
          // A read pinned to a token the server no longer has.
          if (req.consistency?.requirement.case === "atLeastAsFresh" &&
              req.consistency.requirement.value.token === STALE_TOKEN) {
            throw new ConnectError(
              "the specified revision has expired or been garbage collected",
              Code.OutOfRange,
            );
          }
          // Anything else: re-reading at full consistency succeeds. That is the
          // whole point of the recovery -- dropping the stale token suffices.
          return { permissionship: 2 as never }; // HAS_PERMISSION
        },
      });
    },
  });
  return http2.createServer(handler);
}

function rotatedTokenServer(): http2.Http2Server {
  const handler = connectNodeAdapter({
    routes: (router) => {
      router.service(PermissionsService, {
        checkPermission: () => {
          throw new ConnectError("invalid token", Code.Unauthenticated);
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
  // ── 1. OUT_OF_RANGE: discard the stale token, re-read at full ────────
  const oor = standIn();
  const oorPort = await listen(oor);
  const c = createSpiceDBClient(`localhost:${oorPort}`, "some-token", {
    insecure: true,
  });
  try {
    let caught: unknown;
    try {
      await c.checkPermission(atLeast(STALE_TOKEN), DOC);
    } catch (err) {
      caught = err;
    }
    assert(
      caught instanceof OutOfRangeError,
      `a check pinned to a collected ZedToken must surface as OutOfRangeError, not a generic failure a caller has to string-match: got ${String(caught)}`,
    );
    // Clause 2: the underlying error survives the mapping, so the server's own
    // detail stays reachable rather than reduced to a code and a rebuilt string.
    assert(
      caught.cause !== undefined,
      "the underlying error must remain reachable as `cause`",
    );
    console.log("stale ZedToken: OutOfRangeError, server detail preserved");

    // The recovery the rule calls mechanical, in full. Nothing parses a message.
    const result = await c.checkPermission(full(), DOC);
    assert(result.hasPermission(), "the re-read should have returned the permission");
    console.log(
      "recovery: discarded the token, re-read at full consistency, got an answer",
    );
  } finally {
    c.close();
    await new Promise<void>((resolve) => oor.close(() => resolve()));
  }

  // ── 2. UNAUTHENTICATED: refresh credentials, do not page anyone ──────
  const auth = rotatedTokenServer();
  const authPort = await listen(auth);
  const ac = createSpiceDBClient(`localhost:${authPort}`, "rotated-token", {
    insecure: true,
  });
  try {
    let caught: unknown;
    try {
      await ac.checkPermission(full(), DOC);
    } catch (err) {
      caught = err;
    }
    assert(
      caught instanceof UnauthenticatedError,
      `a rejected token must be distinguishable from an internal fault: got ${String(caught)}`,
    );
    // Asserting the negative is the half that would silently rot if every code
    // collapsed into one class.
    assert(
      !(caught instanceof UnavailableError),
      "an auth failure must not also be an unavailable error",
    );
    console.log(
      "rotated token: UnauthenticatedError, distinct from a transport fault",
    );
  } finally {
    ac.close();
    await new Promise<void>((resolve) => auth.close(() => resolve()));
  }

  // ── 3. What the real SpiceDB actually does with a bad preshared key ──
  const bad = createSpiceDBClient(ENDPOINT, "definitely-the-wrong-key", {
    insecure: true,
  });
  try {
    let caught: unknown;
    try {
      await bad.readSchema();
    } catch (err) {
      caught = err;
    }
    assert(
      caught instanceof PermissionDeniedError,
      `SpiceDB rejects a bad preshared key with PERMISSION_DENIED; if this now reports something else, this example's guidance is stale and must be updated: got ${String(caught)}`,
    );
    console.log(
      "real SpiceDB, wrong preshared key: PermissionDeniedError (not UnauthenticatedError)",
    );
  } finally {
    bad.close();
  }

  console.log("error_mapping: both recoveries work without parsing a message");
}

main().catch((err: unknown) => {
  console.error(err);
  process.exit(1);
});
