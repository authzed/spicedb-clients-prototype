/**
 * Example: the opt-in a plaintext connection to a remote host requires
 *
 * Root DESIGN.md, "RULE: Credentials over insecure transport require an
 * explicit opt-in".
 *
 * The failure this rule exists to prevent is mundane and common: a developer
 * copies `insecure: true` out of a localhost example into a staging config, and
 * a long-lived SpiceDB token -- a complete authorization bypass in anyone
 * else's hands -- goes onto the wire in cleartext with nothing signalling that
 * it happened. So `insecure: true` alone is loopback-only, and reaching a
 * remote host over plaintext takes a second, separately-named option the caller
 * cannot supply by accident: `allowInsecureRemoteCredentials`.
 *
 * The sharpest case is the last one. The rule requires the guard's answer to be
 * *the transport's* answer -- here the WHATWG `URL` parser Connect-ES dials
 * with -- rather than a hand-rolled string split. Given
 * `127.0.0.1:443@evil.com`, a last-colon split reads the host as `127.0.0.1`
 * and waves it through, while `URL` reads `127.0.0.1:443` as *userinfo* and the
 * real host as `evil.com`. A client that split the string would hand the token
 * to evil.com believing it was talking to loopback.
 */
import { createSpiceDBClient } from "../../src/index.js";

const ENDPOINT = process.env.SPICEDB_ENDPOINT ?? "localhost:50051";
const TOKEN = process.env.SPICEDB_TOKEN ?? "somerandomkeyhere";

function assert(condition: unknown, message: string): asserts condition {
  if (!condition) {
    throw new Error(message);
  }
}

/**
 * Requires construction to be refused, and the refusal to say why.
 *
 * Note what is NOT asserted: the error's class. This guard throws a plain
 * `Error` from the proto tier, where the Python client raises its own
 * `InvalidArgumentError`. That inconsistency is real and is recorded rather
 * than papered over here -- pinning `SpiceDBError` would fail today, and
 * pinning `Error` would assert nothing, since every error is one.
 */
async function refuses(endpoint: string, what: string): Promise<void> {
  let refused = false;
  try {
    createSpiceDBClient(endpoint, TOKEN, { insecure: true });
  } catch (err) {
    const message = err instanceof Error ? err.message : String(err);
    // The message has to be actionable: name the endpoint, and name the way out.
    assert(
      message.includes(endpoint),
      `${what}: the refusal should name the endpoint, got: ${message}`,
    );
    assert(
      message.includes("allowInsecureRemoteCredentials"),
      `${what}: the refusal should name the opt-in, got: ${message}`,
    );
    refused = true;
  }
  assert(refused, `SECURITY: ${what} was accepted -- ${endpoint}`);
}

async function main(): Promise<void> {
  // ── 1. Loopback plaintext needs no opt-in ────────────────────────────
  //
  // The case the rule deliberately leaves ergonomic: a token on a loopback
  // socket never leaves the machine, so requiring ceremony here would only
  // train developers to reach for the opt-in reflexively.
  const client = createSpiceDBClient(ENDPOINT, TOKEN, { insecure: true });

  // Prove the client is usable, not merely constructed: the transport connects
  // lazily, so a constructor returning a client that could not talk to anything
  // would still satisfy a "did not throw" assertion.
  await client.writeSchema(`definition user {}

definition document {
	relation viewer: user
	permission view = viewer
}`);
  console.log("loopback plaintext: allowed with no opt-in, and works");

  // ── 2. Remote plaintext is refused ───────────────────────────────────
  //
  // No connection is attempted: the refusal happens during construction, so the
  // token never reaches a socket. This is not about whether the host exists --
  // example.com is refused because it is not loopback, full stop.
  await refuses("example.com:50051", "remote plaintext with no opt-in");
  console.log("remote plaintext, no opt-in: refused");

  // ── 3. ...unless the caller says so, by name ─────────────────────────
  //
  // Two options, not one, and that separation is the point. `insecure` selects
  // the plaintext transport; `allowInsecureRemoteCredentials` accepts the
  // credential exposure that follows. "I want plaintext for local dev" and "I
  // accept shipping this token in cleartext to a remote host" are different
  // decisions, and clause 1 forbids one boolean from doing both jobs.
  createSpiceDBClient("example.com:50051", TOKEN, {
    insecure: true,
    allowInsecureRemoteCredentials: true,
  });
  console.log("remote plaintext, explicit opt-in: allowed");

  // ── 4. The spoof a string split would wave through ───────────────────
  //
  // Under URL parsing the authority here is evil.com. Failing closed is what
  // matters: the rule does not promise every client agrees on loopback
  // spellings, but it does require that anything the guard calls loopback is
  // somewhere the transport actually dials on loopback.
  await refuses(
    "127.0.0.1:443@evil.com",
    "an endpoint whose real host is evil.com",
  );
  console.log("userinfo spoof 127.0.0.1:443@evil.com: refused");

  console.log("insecure_opt_in: all four cases behaved as the rule requires");
}

main().catch((err: unknown) => {
  console.error(err);
  process.exit(1);
});
