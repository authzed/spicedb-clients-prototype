import { describe, it, expect, vi, beforeEach } from "vitest";
import { createRouterTransport } from "@connectrpc/connect";
import type { CommonTransportOptions } from "@connectrpc/connect/protocol";
import { PermissionsService } from "../gen/authzed/api/v1/permission_service_pb.js";
import { isLoopbackEndpoint } from "../client.js";

/**
 * Regression tests for root DESIGN.md, "RULE: Credentials over insecure
 * transport require an explicit opt-in".
 *
 * The mock below replaces createGrpcTransport (which would open a real
 * HTTP/2 connection) with Connect's in-memory createRouterTransport, while
 * forwarding the SAME `interceptors` array createSpiceDBClient built --
 * the authorization-header-setting logic under test is completely
 * unmodified production code, just routed to a capturing in-memory
 * service instead of a real socket. Http2SessionManager is similarly
 * wrapped (not replaced) so tests can prove it was never constructed at
 * all for a rejected combination -- a stronger assertion than "the call
 * threw", since an implementation that opened the session and only THEN
 * threw would still pass a bare toThrow() check but would fail this one.
 */

const http2SessionManagerCtor = vi.fn();
const capturedAuth: (string | null)[] = [];

vi.mock("@connectrpc/connect-node", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@connectrpc/connect-node")>();

  class SpyHttp2SessionManager extends actual.Http2SessionManager {
    constructor(...args: ConstructorParameters<typeof actual.Http2SessionManager>) {
      http2SessionManagerCtor(...args);
      super(...args);
    }
  }

  return {
    ...actual,
    Http2SessionManager: SpyHttp2SessionManager,
    createGrpcTransport: (options: { interceptors?: CommonTransportOptions["interceptors"] }) =>
      createRouterTransport(
        (router) => {
          router.service(PermissionsService, {
            async checkBulkPermissions(_req, context) {
              capturedAuth.push(context.requestHeader.get("authorization"));
              return {};
            },
          });
        },
        { transport: { interceptors: options.interceptors } },
      ),
  };
});

/**
 * Endpoints whose URI authority is not what a naive host:port split reads out
 * of them. `new URL("http://127.0.0.1:443@evil.com").origin` is
 * `"http://evil.com"` -- and that origin is exactly what `Http2SessionManager`
 * computes and hands to `http2.connect`. Before the fix, `isLoopbackEndpoint`
 * returned true for this, so an insecure client was built with no opt-in and
 * shipped its bearer token to the attacker-controlled host in cleartext.
 */
const AUTHORITY_SHIFTING_ENDPOINTS = ["127.0.0.1:443@evil.com", "[::1]:443@evil.com"];

describe("isLoopbackEndpoint", () => {
  it.each([
    "localhost:50051", "LOCALHOST:50051", "localhost",
    "127.0.0.1:50051", "127.0.0.1", "127.55.66.77:50051",
    "[::1]:50051", "::1",
    "http://127.0.0.1:50051", "http://localhost:50051", "http://[::1]:50051",
    "https://localhost", "HTTP://127.0.0.1:50051/",
  ])("treats %s as loopback", (endpoint) => {
    expect(isLoopbackEndpoint(endpoint)).toBe(true);
  });

  it.each([
    "example.com:443", "staging.internal:443",
    "10.0.0.5:50051", "8.8.8.8:443", "0.0.0.0:50051",
    // Typosquats/lookalikes: a future refactor toward String#includes or
    // String#endsWith on "localhost"/"127.0.0.1" would wrongly treat these
    // as loopback and reopen a credential leak. Must stay non-loopback
    // under exact-match host comparison.
    "localhost.evil.com:443", "127.0.0.1.evil.com:443", "evil-localhost:443",
    // Authority-shifting targets -- see AUTHORITY_SHIFTING_ENDPOINTS below.
    ...AUTHORITY_SHIFTING_ENDPOINTS,
    "localhost@evil.com",
    "localhost/../evil.com",
    "localhost#@evil.com",
    "localhost?@evil.com",
    "localhost.",
    "localhost :50051",
    "127.0.0.1 :50051",
    // Unix targets are NOT loopback for this client, deliberately, and the
    // first two used to be asserted as loopback above. Node's http2 client
    // cannot dial a UDS path from a baseUrl:
    // `new URL("http://unix:/var/run/spicedb.sock").origin` is
    // `"http://unix"`, so the exemption was handing a bearer token to
    // whatever DNS answers for the name "unix". createSpiceDBClient now
    // refuses these outright -- see the unix-socket test below.
    "unix:/var/run/spicedb.sock",
    "unix:///var/run/spicedb.sock",
    "UNIX:/var/run/spicedb.sock",
    // WHATWG URL treats "\" as "/" for special schemes, so a manual split on
    // these would see a different authority than `new URL` does. Guard and
    // transport happen to agree here (both read "localhost"), so this was
    // never a bypass -- it is fenced off so it cannot become one.
    "localhost\\evil.com",
    "127.0.0.1\\evil.com",
    "localhost\\@evil.com",
    // Malformed URLs are not loopback, whatever host they name.
    "http://127.0.0.1:50051/x", "http://u@127.0.0.1:50051", "ftp://127.0.0.1:1",
    "http://localhost", "http://example.com:50051",
  ])("does not treat %s as loopback", (endpoint) => {
    expect(isLoopbackEndpoint(endpoint)).toBe(false);
  });
});

describe("createSpiceDBClient insecure host guard", () => {
  beforeEach(() => {
    http2SessionManagerCtor.mockClear();
    capturedAuth.length = 0;
  });

  it("refuses a non-loopback endpoint without the opt-in, before any session is created", async () => {
    const { createSpiceDBClient } = await import("../client.js");

    expect(() =>
      createSpiceDBClient("evil.example.com:1234", "super-secret-token", { insecure: true }),
    ).toThrowError(/evil\.example\.com:1234/);
    expect(() =>
      createSpiceDBClient("evil.example.com:1234", "super-secret-token", { insecure: true }),
    ).toThrowError(/allowInsecureRemoteCredentials/);

    expect(http2SessionManagerCtor).not.toHaveBeenCalled();
    expect(capturedAuth).toEqual([]);
  });

  /**
   * The regression test for the loopback-guard bypass. Asserting only that
   * the call throws would be satisfied by an implementation that opens the
   * session, sends the token, and throws afterwards -- so this asserts on the
   * transport instead: `http2SessionManagerCtor` proves the session manager
   * that would compute `new URL(...).origin` and hand it to `http2.connect`
   * was never constructed, and `capturedAuth` proves no authorization header
   * ever reached a service. Same bar as the refusal test above, applied to
   * the inputs that used to slip past the guard entirely.
   */
  it.each(AUTHORITY_SHIFTING_ENDPOINTS)(
    "refuses %s, whose URI authority is a different host than a host:port split reads",
    async (endpoint) => {
      const { createSpiceDBClient } = await import("../client.js");

      // Guard rail on the premise: this endpoint really does dial elsewhere.
      expect(new URL(`http://${endpoint}`).hostname).toBe("evil.com");

      // Refused as malformed (userinfo), ahead of the remote-plaintext policy.
      expect(() =>
        createSpiceDBClient(endpoint, "super-secret-token", { insecure: true }),
      ).toThrowError(/userinfo is not supported/);

      expect(http2SessionManagerCtor).not.toHaveBeenCalled();
      expect(capturedAuth).toEqual([]);
    },
  );

  /**
   * A unix-socket target must be refused outright, not treated as loopback.
   * This transport has no UDS support reachable from a baseUrl, so
   * `new URL("http://unix:/var/run/spicedb.sock").origin` is `"http://unix"`
   * and `Http2SessionManager` hands exactly that to `http2.connect` -- meaning
   * the old "a unix socket never leaves the kernel" exemption was shipping the
   * bearer token to a remote host in cleartext while the guard said
   * "loopback".
   *
   * The refusal is unconditional: TLS and allowInsecureRemoteCredentials are
   * both exercised, because neither makes dialing a host called "unix" the
   * thing the caller asked for. The session-manager spy proves nothing was
   * constructed in any of those combinations.
   */
  it.each([
    ["unix:/var/run/spicedb.sock", { insecure: true }],
    ["unix:///var/run/spicedb.sock", { insecure: true }],
    ["UNIX:/var/run/spicedb.sock", { insecure: true }],
    // The opt-in does not buy a unix target either.
    ["unix:/var/run/spicedb.sock", { insecure: true, allowInsecureRemoteCredentials: true }],
    // Nor does TLS.
    ["unix:/var/run/spicedb.sock", {}],
  ] as const)("refuses unix-socket target %s", async (endpoint, options) => {
    const { createSpiceDBClient } = await import("../client.js");

    expect(() => createSpiceDBClient(endpoint, "super-secret-token", options)).toThrowError(
      /unix-domain-socket/,
    );

    expect(http2SessionManagerCtor).not.toHaveBeenCalled();
    expect(capturedAuth).toEqual([]);
  });

  /**
   * A bare IPv6 literal must produce a WORKING client, not merely satisfy the
   * guard. `"::1"` is item 8 of the loopback contract and an explicit fixture
   * above -- but it is not a legal URL authority, so while the guard bracketed
   * it for its own parse and `createSpiceDBClient` built its `baseUrl` from
   * the raw endpoint, `new URL("http://::1")` threw `Invalid URL` and no
   * client could be created at all. A plaintext endpoint must name its port,
   * so the port-less literals are exercised over TLS (port 443).
   */
  it.each(["[::1]:50051", "[0:0:0:0:0:0:0:1]:50051", "127.0.0.1:50051"])(
    "constructs a plaintext client for IPv6 loopback %s",
    async (endpoint) => {
      const { createSpiceDBClient } = await import("../client.js");

      const client = createSpiceDBClient(endpoint, "test-token", { insecure: true });
      expect(client).toBeDefined();
      client.close();
    },
  );

  it.each([
    ["::1", "https://[::1]"],
    ["[::1]", "https://[::1]"],
    ["0:0:0:0:0:0:0:1", "https://[0:0:0:0:0:0:0:1]"],
  ])("constructs a TLS client for bare IPv6 literal %s on port 443", async (endpoint, baseUrl) => {
    const { createSpiceDBClient } = await import("../client.js");
    http2SessionManagerCtor.mockClear();

    createSpiceDBClient(endpoint, "test-token").close();
    expect(http2SessionManagerCtor.mock.calls[0]![0]).toBe(baseUrl);
  });

  it.each(["localhost", "127.0.0.1", "::1", "[::1]", "0:0:0:0:0:0:0:1"])(
    "refuses port-less plaintext endpoint %s before any session is created",
    async (endpoint) => {
      const { createSpiceDBClient, InvalidEndpointError } = await import("../client.js");
      http2SessionManagerCtor.mockClear();

      expect(() => createSpiceDBClient(endpoint, "test-token", { insecure: true })).toThrowError(
        InvalidEndpointError,
      );
      expect(() => createSpiceDBClient(endpoint, "test-token", { insecure: true })).toThrowError(
        /a plaintext endpoint needs an explicit port, e\.g\. localhost:50051/,
      );
      expect(http2SessionManagerCtor).not.toHaveBeenCalled();
    },
  );

  it("allows a loopback endpoint with no opt-in, and actually sends the token", async () => {
    const { createSpiceDBClient } = await import("../client.js");

    const client = createSpiceDBClient("localhost:50051", "test-token", { insecure: true });
    await client.permissions.checkBulkPermissions({});
    client.close();

    expect(capturedAuth).toEqual(["Bearer test-token"]);
  });

  it("allows a non-loopback endpoint when allowInsecureRemoteCredentials is true, and sends the token", async () => {
    const { createSpiceDBClient } = await import("../client.js");

    const client = createSpiceDBClient("evil.example.com:1234", "remote-token", {
      insecure: true,
      allowInsecureRemoteCredentials: true,
    });
    await client.permissions.checkBulkPermissions({});
    client.close();

    expect(capturedAuth).toEqual(["Bearer remote-token"]);
  });
});

describe("createSpiceDBClient endpoint forms", () => {
  beforeEach(() => {
    http2SessionManagerCtor.mockClear();
    capturedAuth.length = 0;
  });

  it.each([
    ["http://127.0.0.1:50051", { insecure: true }, "http://127.0.0.1:50051"],
    ["http://127.0.0.1:50051/", { insecure: true }, "http://127.0.0.1:50051"],
    ["http://[::1]:50051", { insecure: true }, "http://[::1]:50051"],
    ["https://spicedb.example.com", {}, "https://spicedb.example.com"],
    ["https://spicedb.example.com:8443", {}, "https://spicedb.example.com:8443"],
    ["spicedb.example.com:443", {}, "https://spicedb.example.com:443"],
  ] as const)("dials %s through the parsed authority", async (endpoint, options, baseUrl) => {
    const { createSpiceDBClient } = await import("../client.js");
    createSpiceDBClient(endpoint, "t", options).close();
    expect(http2SessionManagerCtor).toHaveBeenCalledTimes(1);
    expect(http2SessionManagerCtor.mock.calls[0]![0]).toBe(baseUrl);
  });

  it.each([
    ["http://127.0.0.1:50051", {}, /http:\/\/ selects plaintext, but this client was configured for TLS/],
    ["https://127.0.0.1:50051", { insecure: true }, /https:\/\/ selects TLS, but this client was configured for plaintext/],
    ["http://localhost", { insecure: true }, /needs an explicit port/],
    ["http://127.0.0.1:50051/x", { insecure: true }, /a path is not supported/],
    ["http://127.0.0.1:50051?a=b", { insecure: true }, /a query is not supported/],
    ["http://127.0.0.1:50051#f", { insecure: true }, /a fragment is not supported/],
    ["http://u@127.0.0.1:50051", { insecure: true }, /userinfo is not supported/],
    ["localhost:http", { insecure: true }, /the port "http" is not a number/],
    ["ftp://host:1", { insecure: true }, /unsupported scheme "ftp"/],
    // Malformed remote endpoints are reported as malformed, not as a
    // remote-plaintext refusal.
    ["http://example.com:50051/x", { insecure: true }, /a path is not supported/],
  ] as const)("refuses %s before any session is created", async (endpoint, options, problem) => {
    const { createSpiceDBClient, InvalidEndpointError } = await import("../client.js");
    let error: unknown;
    try {
      createSpiceDBClient(endpoint, "t", options);
    } catch (e) {
      error = e;
    }
    expect(error).toBeInstanceOf(InvalidEndpointError);
    expect((error as Error).message).toMatch(problem);
    expect((error as Error).message).toMatch(/expected host:port, http:\/\/host:port/);
    expect(http2SessionManagerCtor).not.toHaveBeenCalled();
  });

  it("applies the remote-plaintext policy to a well-formed remote http:// URL", async () => {
    const { createSpiceDBClient, InsecureRemoteHostError } = await import("../client.js");
    expect(() => createSpiceDBClient("http://example.com:50051", "t", { insecure: true })).toThrowError(
      InsecureRemoteHostError,
    );
    expect(() => createSpiceDBClient("spicedb:50051", "t", { insecure: true })).toThrowError(
      'spicedb: refusing to send credentials over plaintext to non-loopback endpoint "spicedb:50051": ' +
        "use TLS (omit insecure), or, for a remote plaintext server such as a Docker Compose service, " +
        "pass both insecure: true and allowInsecureRemoteCredentials: true",
    );
  });

  it("exposes the scheme for the idiomatic layer", async () => {
    const { endpointScheme } = await import("../client.js");
    expect(endpointScheme("http://localhost:50051")).toBe("http");
    expect(endpointScheme("HTTPS://spicedb.example.com")).toBe("https");
    expect(endpointScheme("localhost:50051")).toBeUndefined();
    expect(endpointScheme("ftp://host:1")).toBeUndefined();
  });
});
