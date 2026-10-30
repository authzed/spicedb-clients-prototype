import { createClient, type Client, type Transport } from "@connectrpc/connect";
import { createGrpcTransport, Http2SessionManager } from "@connectrpc/connect-node";
import type { SecureClientSessionOptions } from "node:http2";
import { PermissionsService } from "./gen/authzed/api/v1/permission_service_pb.js";
import { SchemaService } from "./gen/authzed/api/v1/schema_service_pb.js";
import { WatchService } from "./gen/authzed/api/v1/watch_service_pb.js";
import { ExperimentalService } from "./gen/authzed/api/v1/experimental_service_pb.js";
import { RelationshipsService } from "./gen/authzed/api/materialize/v0/relationships_pb.js";
import { WatchPermissionSetsService } from "./gen/authzed/api/materialize/v0/watchpermissionsets_pb.js";
import { WatchPermissionsService } from "./gen/authzed/api/materialize/v0/watchpermissions_pb.js";
import { RoaringLookupResourcesService } from "./gen/authzed/api/materialize/v0/roaringlookupresources_pb.js";

/**
 * Caller-supplied TLS trust material for the secure path.
 *
 * Why this exists. Root DESIGN.md, "RULE: A system-TLS constructor must reach
 * a real server", requires the default secure path to delegate to the
 * ecosystem's default trust source -- for Connect-ES over Node http2, that is
 * whatever `node:tls` uses when no `ca` is given -- and names the hazard that
 * leaves visible: Node ships a bundled Mozilla root store, so a CA an operator
 * installed in the host's own trust store is NOT honoured here. That rule
 * permits delegating to the bundled set precisely *because* a caller can
 * supply their own material instead. These options are what makes that true;
 * without them a SpiceDB fronted by a private or corporate CA was unreachable.
 *
 * Each field is passed straight through to the corresponding `node:tls`
 * option (`ca`, `cert`, `key`) on the HTTP/2 connection, and is typed as
 * exactly what Node accepts there -- a PEM string, a Buffer, or an array of
 * either -- so this cannot drift from what the transport actually supports.
 */
export interface TlsOptions {
  /**
   * Root certificate(s) used to verify SpiceDB's certificate, in place of
   * Node's bundled roots. Supplying this REPLACES the default trust store for
   * this client rather than adding to it -- that is `node:tls`'s behavior, and
   * it is generally what a deployment pinning a private CA wants.
   */
  caCert?: SecureClientSessionOptions["ca"];
  /**
   * The client's own certificate chain, for a server that requires mutual
   * TLS. Must be supplied together with {@link TlsOptions.clientKey}; either
   * half alone is refused, since neither is usable without the other.
   */
  clientCert?: SecureClientSessionOptions["cert"];
  /**
   * The private key for {@link TlsOptions.clientCert}. Must be supplied
   * together with it.
   */
  clientKey?: SecureClientSessionOptions["key"];
}

/**
 * Optional configuration for the SpiceDB proto client.
 */
export interface ClientOptions {
  /**
   * Use plaintext (insecure) connection instead of TLS.
   *
   * By itself, this only permits a plaintext connection to a loopback
   * endpoint (localhost, 127.0.0.0/8, or ::1) -- see
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
  /**
   * Caller-supplied TLS trust material -- a private CA to verify SpiceDB
   * against, and optionally a client certificate for mutual TLS. See
   * {@link TlsOptions}.
   *
   * This never decides *whether* TLS is used: {@link ClientOptions.insecure}
   * alone does that, and combining the two is refused rather than silently
   * ignored (see createSpiceDBClient).
   */
  tls?: TlsOptions;
  /** Additional headers to include in every request. */
  headers?: Record<string, string>;
}

/**
 * Reports whether the connection this client would actually open for
 * `endpoint` terminates on a loopback destination: the literal hostname
 * "localhost", an IP in 127.0.0.0/8, or the IPv6 loopback ::1.
 *
 * Unix-domain-socket targets are NOT in that list, unlike the equivalent
 * guard in the Go, Python and Ruby clients. Those clients' transports
 * genuinely dial a UDS path; this one cannot. `new URL("http://unix:/var/
 * run/spicedb.sock").origin` is `"http://unix"`, and that origin is what
 * `Http2SessionManager` hands to `http2.connect` -- so a "unix:" endpoint
 * here resolves the DNS name `unix`. createSpiceDBClient below refuses such
 * an endpoint outright rather than letting this function call it loopback.
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
 * parsing (`@`, `/`, `\`, `?`, `#`, whitespace) is refused outright: a
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
/**
 * Returns the URL authority the transport dials for `endpoint`.
 *
 * This exists so `isLoopbackEndpoint` and `createSpiceDBClient` cannot
 * disagree about what is being connected to. It brackets a bare IPv6 literal
 * (recognized by holding more than one ":", which no `host:port` form and no
 * real hostname ever does): `"::1"` is an ordinary way to name the loopback
 * host and an explicit part of this client's supported set, but it is not a
 * legal URL authority, so `new URL("http://::1")` throws `Invalid URL`. The
 * guard used to bracket it for its own parse while `createSpiceDBClient` built
 * its `baseUrl` from the raw endpoint, so `"::1"` passed the guard and then
 * failed to construct a client at all.
 *
 * Anything already bracketed, or not an IPv6 literal, is returned unchanged.
 */
function transportAuthority(endpoint: string): string {
  return !endpoint.startsWith("[") && (endpoint.match(/:/g) ?? []).length > 1
    ? `[${endpoint}]`
    : endpoint;
}

/** The scheme of an endpoint written as a URL: `http` is plaintext, `https` TLS. */
export type EndpointScheme = "http" | "https";

const ENDPOINT_FORMS =
  "expected host:port, http://host:port (plaintext) or https://host[:port] (TLS)";

/**
 * Thrown when an endpoint is not one of the accepted forms (`host:port`,
 * `http://host:port`, `https://host[:port]`), is malformed (path, query,
 * fragment, userinfo, non-numeric port, unknown scheme), or carries a scheme
 * that disagrees with the transport requested. The message names the problem
 * and the accepted forms.
 */
export class InvalidEndpointError extends Error {
  constructor(endpoint: string, reason: string) {
    super(`spicedb: invalid endpoint "${endpoint}": ${reason}; ${ENDPOINT_FORMS}`);
    this.name = "InvalidEndpointError";
  }
}

interface ParsedEndpoint {
  /** Undefined for the `host:port` form. */
  readonly scheme?: EndpointScheme;
  /** The authority the transport dials (IPv6 literals bracketed). */
  readonly authority: string;
  readonly hasPort: boolean;
}

/**
 * Parses an endpoint into the authority the transport dials, or throws
 * InvalidEndpointError naming the problem. One trailing "/" after a URL's
 * authority is accepted, as deployment URLs often carry one.
 */
function parseEndpoint(endpoint: string): ParsedEndpoint {
  const fail = (reason: string): never => {
    throw new InvalidEndpointError(endpoint, reason);
  };
  if (/\s/.test(endpoint)) fail("whitespace is not allowed");
  let scheme: EndpointScheme | undefined;
  let rest = endpoint;
  const url = /^([A-Za-z][A-Za-z0-9+.-]*):\/\/(.*)$/s.exec(endpoint);
  if (url) {
    const name = url[1]!.toLowerCase();
    if (name !== "http" && name !== "https") fail(`unsupported scheme "${url[1]}"`);
    scheme = name as EndpointScheme;
    rest = url[2]!.endsWith("/") ? url[2]!.slice(0, -1) : url[2]!;
  }
  if (rest === "") fail("no host");
  if (rest.includes("@")) fail("userinfo is not supported");
  if (rest.includes("#")) fail("a fragment is not supported");
  if (rest.includes("?")) fail("a query is not supported");
  if (/[/\\]/.test(rest)) fail("a path is not supported");

  let host: string;
  let port: string | undefined;
  if (rest.startsWith("[")) {
    const close = rest.indexOf("]");
    if (close < 0) fail("unterminated IPv6 literal");
    host = rest.slice(1, close);
    const after = rest.slice(close + 1);
    if (after !== "") {
      if (!after.startsWith(":")) fail("not a valid host and port");
      port = after.slice(1);
    }
  } else if ((rest.match(/:/g) ?? []).length > 1) {
    // A bare IPv6 literal: no port can follow it unbracketed.
    if (scheme !== undefined) fail("an IPv6 address in a URL must be bracketed");
    host = rest;
  } else {
    const colon = rest.indexOf(":");
    host = colon < 0 ? rest : rest.slice(0, colon);
    port = colon < 0 ? undefined : rest.slice(colon + 1);
  }
  if (host === "") fail("no host");
  if (port !== undefined) {
    if (!/^\d+$/.test(port)) fail(`the port "${port}" is not a number`);
    if (Number(port) < 1 || Number(port) > 65535) fail(`the port ${port} is out of range`);
  }
  if (scheme === "http" && port === undefined)
    fail("an http:// endpoint needs an explicit port (SpiceDB's gRPC port is usually 50051)");

  const authority = transportAuthority(rest);
  try {
    new URL(`http://${authority}`);
  } catch {
    fail("not a valid host and port");
  }
  return { scheme, authority, hasPort: port !== undefined };
}

/**
 * The scheme of an endpoint written as an `http://` or `https://` URL, or
 * undefined for the `host:port` form (and for anything construction
 * rejects). The idiomatic client uses it to let the scheme select plaintext
 * or TLS when the caller did not choose.
 */
export function endpointScheme(endpoint: string): EndpointScheme | undefined {
  try {
    return parseEndpoint(endpoint).scheme;
  } catch {
    return undefined;
  }
}

/**
 * Thrown when a plaintext connection to a non-loopback endpoint is requested
 * without `allowInsecureRemoteCredentials`.
 *
 * A named class rather than a bare `Error` so the idiomatic client can map this
 * refusal onto its own error type without matching on prose -- see root
 * DESIGN.md, "RULE: Credentials over insecure transport require an explicit
 * opt-in", clause 4.
 */
/**
 * The remote-plaintext refusal, naming every option the caller must set to
 * allow it, spelled as that caller writes them. The idiomatic layer passes its
 * own spellings.
 */
export function remotePlaintextRefusal(
  endpoint: string,
  tlsChoice: string,
  plaintextOption: string,
  remoteOption: string,
): string {
  return (
    `spicedb: refusing to send credentials over plaintext to non-loopback endpoint ` +
    `"${endpoint}": use TLS (${tlsChoice}), or, for a remote plaintext server such as ` +
    `a Docker Compose service, pass both ${plaintextOption} and ${remoteOption}`
  );
}

export class InsecureRemoteHostError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "InsecureRemoteHostError";
  }
}

export function isLoopbackEndpoint(endpoint: string): boolean {
  // There is deliberately no "unix:" exemption here -- see the doc comment
  // above, and the unconditional refusal in createSpiceDBClient below.

  // Exactly the authority the transport will dial -- see parseEndpoint,
  // which createSpiceDBClient uses to build its baseUrl. Using one function
  // for both is the point: while this bracketed a bare IPv6 literal and
  // createSpiceDBClient did not, "::1" passed the guard and then died in
  // `new URL()` with "Invalid URL". An http:// or https:// URL is judged by
  // its host, so "http://127.0.0.1:50051" is loopback.
  let authority: string;
  try {
    authority = parseEndpoint(endpoint).authority;
  } catch {
    return false;
  }

  // Fail closed on any character left in the authority that can shift which
  // part of the string the URL parser treats as the host: "@" (userinfo),
  // "/" (path), "\" (which WHATWG URL treats as "/" for special schemes, so
  // "localhost\evil.com" has the authority "localhost"), "?" (query), "#"
  // (fragment), whitespace. parseEndpoint already refuses all of them --
  // deliberately redundant, so a future edit to the parser that reaches for
  // a manual split cannot reopen an authority-shifting bypass here.
  if (/[@/\\?#]|\s/.test(authority)) {
    return false;
  }

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
  readonly materialize: Client<typeof RoaringLookupResourcesService>;
  readonly materializeRelationships: Client<typeof RelationshipsService>;
  readonly permissionSets: Client<typeof WatchPermissionSetsService>;
  /** @deprecated Use permissionSets for new materialization consumers. */
  readonly watchPermissions: Client<typeof WatchPermissionsService>;

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
    this.materialize = createClient(RoaringLookupResourcesService, transport);
    this.materializeRelationships = createClient(RelationshipsService, transport);
    this.permissionSets = createClient(WatchPermissionSetsService, transport);
    this.watchPermissions = createClient(WatchPermissionsService, transport);
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
  // Refused unconditionally -- ahead of the credential guard below, and
  // regardless of TLS or of allowInsecureRemoteCredentials -- because this
  // transport cannot do what such an endpoint asks for. Connect-ES over
  // Node http2 has no unix-domain-socket support reachable from a baseUrl:
  // `new URL("http://unix:/var/run/spicedb.sock").origin` is `"http://unix"`,
  // and Http2SessionManager hands exactly that to http2.connect. So the
  // endpoint that looks local would resolve the DNS name `unix` and ship the
  // bearer token there. Failing loudly is the only honest answer; silently
  // dialing a host called "unix" is not. Matched case-insensitively because
  // URL lower-cases the scheme, so "UNIX:" reaches the same place.
  if (/^unix:/i.test(endpoint)) {
    throw new InvalidEndpointError(
      endpoint,
      `unix-domain-socket targets are not supported by this client's transport ` +
        `(Node's http2 client would parse "unix" as a DNS hostname and connect there, not to the socket path)`,
    );
  }

  // The endpoint's form is checked before the credential guard below, so a
  // malformed endpoint is reported as such and the remote-plaintext message
  // appears only for a well-formed remote host. A URL's scheme must agree
  // with the requested transport.
  const parsed = parseEndpoint(endpoint);
  if (parsed.scheme === "http" && !options?.insecure) {
    throw new InvalidEndpointError(
      endpoint,
      "http:// selects plaintext, but this client was configured for TLS",
    );
  }
  if (parsed.scheme === "https" && options?.insecure) {
    throw new InvalidEndpointError(
      endpoint,
      "https:// selects TLS, but this client was configured for plaintext",
    );
  }
  // No client silently dials port 80: a plaintext endpoint, bare or URL,
  // names its port. A TLS endpoint without one uses 443.
  if (options?.insecure && !parsed.hasPort) {
    throw new InvalidEndpointError(
      endpoint,
      "a plaintext endpoint needs an explicit port, e.g. localhost:50051",
    );
  }

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
    throw new InsecureRemoteHostError(
      remotePlaintextRefusal(endpoint, "omit insecure", "insecure: true", "allowInsecureRemoteCredentials: true"),
    );
  }

  // Trust material must never become a second, quieter route to a plaintext
  // transport. A plaintext connection performs no TLS handshake, so
  // http2.connect would ignore `ca`/`cert`/`key` entirely and put the bearer
  // token on the wire in cleartext behind a call site reading
  // `{ insecure: true, tls: { caCert } }` -- which looks, at a glance, like
  // TLS was configured. That is exactly the failure root DESIGN.md, "RULE:
  // Credentials over insecure transport require an explicit opt-in", exists
  // to prevent, so the combination is refused rather than ignored. It refuses
  // rather than "helpfully" turning TLS on: silently upgrading the transport
  // is just as much of a surprise in the other direction.
  const suppliedTlsMaterial = (
    [
      ["caCert", options?.tls?.caCert],
      ["clientCert", options?.tls?.clientCert],
      ["clientKey", options?.tls?.clientKey],
    ] as const
  )
    .filter(([, value]) => value !== undefined)
    .map(([name]) => name);

  if (options?.insecure && suppliedTlsMaterial.length > 0) {
    throw new Error(
      `spicedb: refusing to build a client with insecure: true and TLS material ` +
        `(${suppliedTlsMaterial.join(", ")}): a plaintext connection performs no TLS ` +
        `handshake, so the material would be ignored and everything -- including the ` +
        `bearer token -- would be sent in cleartext. Drop insecure to use TLS, or drop ` +
        `the tls option to connect in plaintext`,
    );
  }

  // Either half of a client identity is unusable alone. node:tls fails later
  // and from a layer with no idea which option the caller got wrong; naming
  // it here is the whole difference.
  if (
    (options?.tls?.clientCert === undefined) !==
    (options?.tls?.clientKey === undefined)
  ) {
    const missing =
      options?.tls?.clientKey === undefined ? "clientKey" : "clientCert";
    const present =
      options?.tls?.clientKey === undefined ? "clientCert" : "clientKey";
    throw new Error(
      `spicedb: tls.${present} was supplied without tls.${missing}: mutual TLS needs ` +
        `both halves of the client identity, and neither is usable alone`,
    );
  }

  // Constructed explicitly (rather than left for createGrpcTransport to
  // build internally) so SpiceDBProtoClient.close() has a handle to abort
  // -- createGrpcTransport accepts a pre-built sessionManager via
  // GrpcTransportOptions precisely to support this.
  //
  // transportAuthority, not the raw endpoint: a bare IPv6 literal must be
  // bracketed to be a legal URL authority, and it is the same function
  // isLoopbackEndpoint vetted, so the two cannot disagree about where this
  // connection goes.
  const baseUrl = options?.insecure
    ? `http://${parsed.authority}`
    : `https://${parsed.authority}`;
  // Third constructor argument, NOT createGrpcTransport's `nodeOptions`.
  // Connect-ES documents that supplying a `sessionManager` "makes nodeOptions
  // as well as the HTTP/2 session options ineffective", and this client always
  // supplies one (see the comment above) -- so trust material handed to
  // `nodeOptions` here would be silently dropped and every private-CA
  // handshake would still fail, with nothing in the call site to suggest why.
  // Http2SessionManager passes this object to `http2.connect`, which is where
  // `ca`/`cert`/`key` take effect.
  //
  // `undefined` rather than an all-undefined object when no material was
  // supplied, so the default secure path is provably untouched: this library
  // must select no root set of its own, which clause 1 of "RULE: A system-TLS
  // constructor must reach a real server" prohibits.
  const sessionManager = new Http2SessionManager(
    baseUrl,
    undefined,
    options?.tls
      ? {
          ca: options.tls.caCert,
          cert: options.tls.clientCert,
          key: options.tls.clientKey,
        }
      : undefined,
  );
  const transport = createGrpcTransport({
    baseUrl,
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
