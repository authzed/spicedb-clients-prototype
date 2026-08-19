import { describe, it, expect } from "vitest";
import { createSpiceDBClient } from "../client.js";

/**
 * Regression coverage for root DESIGN.md, "RULE: Credentials over insecure
 * transport require an explicit opt-in", at the idiomatic layer.
 *
 * The proto-layer test suite (@spicedb/proto's
 * "createSpiceDBClient insecure host guard") is what proves the token
 * itself never reaches the wire -- via a real in-memory router transport
 * and a wrapped Http2SessionManager that's asserted never constructed for
 * a rejected combination. These tests prove createSpiceDBClient here
 * actually reaches, and propagates options into, that same guard.
 */
describe("createSpiceDBClient insecure host guard (idiomatic layer)", () => {
  it("refuses a non-loopback endpoint without the opt-in", () => {
    expect(() =>
      createSpiceDBClient("evil.example.com:1234", "test-token", { insecure: true }),
    ).toThrowError(/allowInsecureRemoteCredentials/);
  });

  /**
   * The bypass shapes, at the public entry point. The proto layer holds the
   * full fixture set and the "token never sent" proof; these are the ones
   * that must not slip through createSpiceDBClient here.
   */
  it.each([
    "127.0.0.1:443@evil.com",
    "[::1]:443@evil.com",
    "[::1]:0@127.0.0.1:19999",
    "localhost@evil.com",
  ])("refuses %s, whose URI authority is a different host than a host:port split reads", (endpoint) => {
    expect(() =>
      createSpiceDBClient(endpoint, "test-token", { insecure: true }),
    ).toThrowError(/allowInsecureRemoteCredentials/);
  });

  /**
   * A unix-socket endpoint is refused, not silently turned into a DNS lookup
   * for the hostname "unix". Node's http2 client cannot dial a UDS path from
   * a baseUrl.
   */
  it.each(["unix:/var/run/spicedb.sock", "unix:///var/run/spicedb.sock"])(
    "refuses unix-socket target %s",
    (endpoint) => {
      expect(() => createSpiceDBClient(endpoint, "test-token", { insecure: true })).toThrowError(
        /unix-domain-socket/,
      );
    },
  );

  it("allows a loopback endpoint with no opt-in", () => {
    expect(() =>
      createSpiceDBClient("localhost:50051", "test-token", { insecure: true }),
    ).not.toThrow();
  });

  it("allows a non-loopback endpoint when allowInsecureRemoteCredentials is true", () => {
    expect(() =>
      createSpiceDBClient("evil.example.com:1234", "test-token", {
        insecure: true,
        allowInsecureRemoteCredentials: true,
      }),
    ).not.toThrow();
  });
});
