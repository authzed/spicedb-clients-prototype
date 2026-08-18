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

describe("isLoopbackEndpoint", () => {
  it.each([
    "localhost:50051", "LOCALHOST:50051", "localhost",
    "127.0.0.1:50051", "127.0.0.1", "127.55.66.77:50051",
    "[::1]:50051", "::1",
    "unix:/var/run/spicedb.sock", "unix:///var/run/spicedb.sock",
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
