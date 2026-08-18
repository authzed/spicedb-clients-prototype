import { createClient, type Client, type Transport } from "@connectrpc/connect";
import { createGrpcTransport, Http2SessionManager } from "@connectrpc/connect-node";
import { PermissionsService } from "./gen/authzed/api/v1/permission_service_pb.js";
import { SchemaService } from "./gen/authzed/api/v1/schema_service_pb.js";
import { WatchService } from "./gen/authzed/api/v1/watch_service_pb.js";
import { ExperimentalService } from "./gen/authzed/api/v1/experimental_service_pb.js";

/**
 * Optional configuration for the SpiceDB proto client.
 */
export interface ClientOptions {
  /**
   * Use plaintext (insecure) connection instead of TLS.
   *
   * By itself, this only permits a plaintext connection to a loopback
   * endpoint (localhost, 127.0.0.0/8, ::1, or a unix socket target) -- see
   * root DESIGN.md, "RULE: Credentials over insecure transport require an
   * explicit opt-in". For a non-loopback endpoint, also pass
   * `allowInsecureRemoteCredentials: true`.
   */
  insecure?: boolean;
  /**
   * Explicit, separately named opt-in permitting `insecure: true` to target
   * a non-loopback endpoint. Named and separate from `insecure` on purpose:
   * the rule requires an option a reader cannot mistake for a default, not
   * a boolean that does double duty as the plaintext-transport switch. Set
   * this to `true` only if you genuinely mean to send a bearer token in
   * cleartext to a remote host.
   */
  allowInsecureRemoteCredentials?: boolean;
  /** Additional headers to include in every request. */
  headers?: Record<string, string>;
}

/**
 * Reports whether the connection this client would actually open for
 * `endpoint` terminates on a loopback destination: the literal hostname
 * "localhost", an IP in 127.0.0.0/8, the IPv6 loopback ::1, or a unix
 * domain socket target (a "unix:" prefix). A unix socket never leaves the
 * host's kernel, so it is loopback for this check even though it has no IP
 * at all.
 *
 * That wording is deliberate. This function does not answer "does this
 * string look like it names a loopback host"; it answers "will the
 * transport dial loopback". Those are the same question only if this
 * function and the transport agree on where the host ends and the rest of
 * the target begins -- and a hand-rolled string split will always disagree
 * with a URI parser somewhere. It used to: given
 * `"127.0.0.1:443@evil.com"` a last-colon split yields host "127.0.0.1"
 * and reports loopback, while createSpiceDBClient below hands
 * `` `http://${endpoint}` `` to `Http2SessionManager`, which does
 * `new URL(url).origin` -- reading "127.0.0.1:443" as URI *userinfo* and
 * yielding `"http://evil.com"`, the authority it then passes straight to
 * `http2.connect`. So the bearer token went to evil.com in cleartext with
 * this function reporting "loopback".
 *
 * So the host is derived by building the exact URL createSpiceDBClient
 * dials and asking `URL` -- the same parser `Http2SessionManager` uses --
 * for its hostname. There is one parse, so guard and transport cannot
 * disagree. Before that, anything that could move the authority under URI
 * parsing (`@`, `/`, `?`, `#`, whitespace) is refused outright: a
 * legitimate SpiceDB target contains none of those, and failing closed on a
 * weird endpoint is the correct trade for a credential leak.
 *
 * This is the exemption in root DESIGN.md, "RULE: Credentials over
 * insecure transport require an explicit opt-in": loopback is the reason
 * `insecure: true` exists at all (local development, docker-compose, CI),
 * so it must keep working with no extra ceremony. Anything else requires
 * `allowInsecureRemoteCredentials: true` -- see createSpiceDBClient below.
 *
 * Exported (but not re-exported from index.ts) so tests can exercise it
 * directly; not part of the package's public API surface.
 */
export function isLoopbackEndpoint(endpoint: string): boolean {
  // Checked first, and only on the raw string: a unix target is not a URI
  // authority at all (it carries a filesystem path, so it legitimately
  // contains the "/" the reserved-character check below refuses), and it
  // never leaves the host's kernel regardless of what the path says.
  if (endpoint.startsWith("unix:")) {
    return true;
  }

  // Fail closed on any character that can shift which part of the string
  // the URL parser treats as the authority: "@" (userinfo), "/" (path),
  // "?" (query), "#" (fragment), whitespace. Redundant with the URL parse
  // below -- deliberately so. The parse is what makes this function
  // correct; this is what keeps it correct if some future edit ever
  // reaches for a manual split again.
  if (/[@/?#]|\s/.test(endpoint)) {
    return false;
  }

  // A bare IPv6 literal ("::1") is not a legal URL authority -- brackets
  // are the only form the transport can dial -- so bracket it (recognized
  // by holding more than one ":", which no host:port form and no real
  // hostname ever does) and let the one parser below judge it, rather than
  // special-casing it out of the parse entirely.
  const authority =
    !endpoint.startsWith("[") && (endpoint.match(/:/g) ?? []).length > 1
      ? `[${endpoint}]`
      : endpoint;

  // The scheme is "http" because this guard only ever gates the insecure
  // path; either way, scheme does not affect how the authority is parsed.
  let url: URL;
  try {
    url = new URL(`http://${authority}`);
  } catch {
    return false;
  }

  // URL.hostname keeps the brackets on an IPv6 literal, and has already
  // lower-cased and normalized the host (e.g. "127.1" -> "127.0.0.1")
  // exactly as the transport sees it.
  let host = url.hostname;
  if (host.startsWith("[") && host.endsWith("]")) {
    host = host.slice(1, -1);
  }

  if (host.toLowerCase() === "localhost") {
    return true;
  }

  // IPv4 literal -- parsed by hand, never via DNS.
  const ipv4Match = host.match(/^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/);
  if (ipv4Match) {
    const octets = ipv4Match.slice(1).map(Number);
    if (octets.every((o) => o >= 0 && o <= 255)) {
      return octets[0] === 127;
    }
    return false;
  }

  // IPv6 literal -- recognized purely by containing ':', which no real
  // hostname ever does, then matched against the loopback address ::1
  // (with or without leading-zero/compressed variants normalized away by
  // string comparison against the two forms that matter here).
  if (host.includes(":")) {
    const normalized = host.toLowerCase();
    return normalized === "::1" || normalized === "0:0:0:0:0:0:0:1";
  }

  return false;
}

/**
 * SpiceDBProtoClient wraps all generated Connect-ES service clients.
 */
export class SpiceDBProtoClient {
  readonly permissions: Client<typeof PermissionsService>;
  readonly schema: Client<typeof SchemaService>;
  readonly watch: Client<typeof WatchService>;
  readonly experimental: Client<typeof ExperimentalService>;

  private readonly sessionManager?: Http2SessionManager;
  private closed = false;

  // sessionManager is optional so tests can construct a SpiceDBProtoClient
  // directly around a fake in-memory Transport (e.g. Connect's
  // createRouterTransport) with nothing to close -- createSpiceDBClient,
  // the only production entry point, always supplies one.
  constructor(transport: Transport, sessionManager?: Http2SessionManager) {
    this.permissions = createClient(PermissionsService, transport);
    this.schema = createClient(SchemaService, transport);
    this.watch = createClient(WatchService, transport);
    this.experimental = createClient(ExperimentalService, transport);
    this.sessionManager = sessionManager;
  }

  /**
   * Closes the underlying HTTP/2 connection. Idempotent -- safe to call
   * more than once.
   *
   * `createGrpcTransport` opens its HTTP/2 session lazily and keeps it
   * alive for the life of the process by default; without an explicit
   * close, every streaming call this client makes shares a connection that
   * is never released deterministically. See root DESIGN.md, "RULE:
   * Abandoning a stream must release it".
   */
  close(): void {
    if (this.closed) {
      return;
    }
    this.closed = true;
    this.sessionManager?.abort();
  }
}

/**
 * Creates a SpiceDBProtoClient connected to the given endpoint with bearer
 * token authentication.
 */
export function createSpiceDBClient(
  endpoint: string,
  token: string,
  options?: ClientOptions,
): SpiceDBProtoClient {
  // See root DESIGN.md, "RULE: Credentials over insecure transport require
  // an explicit opt-in". Refuse before the session manager, transport, or
  // authorization-header interceptor below are ever created, so a bearer
  // token can never reach the wire for a rejected combination -- Connect-ES
  // has no built-in "refuse call credentials over an insecure channel"
  // check the way some other language bindings do, so nothing else here
  // stops that interceptor from shipping a token to an arbitrary insecure
  // host.
  if (
    options?.insecure &&
    !options?.allowInsecureRemoteCredentials &&
    !isLoopbackEndpoint(endpoint)
  ) {
    throw new Error(
      `spicedb: refusing to send credentials over an insecure (plaintext) connection to ` +
        `non-loopback endpoint "${endpoint}": use TLS (omit insecure), or pass ` +
        `allowInsecureRemoteCredentials: true if you intend to send a bearer token in ` +
        `cleartext to a remote host`,
    );
  }

  // Constructed explicitly (rather than left for createGrpcTransport to
  // build internally) so SpiceDBProtoClient.close() has a handle to abort
  // -- createGrpcTransport accepts a pre-built sessionManager via
  // GrpcTransportOptions precisely to support this.
  const sessionManager = new Http2SessionManager(
    options?.insecure ? `http://${endpoint}` : `https://${endpoint}`,
  );
  const transport = createGrpcTransport({
    baseUrl: options?.insecure ? `http://${endpoint}` : `https://${endpoint}`,
    sessionManager,
    interceptors: [
      // Sets the bearer token unconditionally, regardless of transport
      // security -- the endpoint has already been proven loopback (or
      // explicitly allowed) by the guard above.
      (next) => (req) => {
        req.header.set("authorization", `Bearer ${token}`);
        if (options?.headers) {
          for (const [key, value] of Object.entries(options.headers)) {
            req.header.set(key, value);
          }
        }
        return next(req);
      },
    ],
  });
  return new SpiceDBProtoClient(transport, sessionManager);
}
