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
