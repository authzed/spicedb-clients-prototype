import { describe, it, expect } from "vitest";
import { createSpiceDBClient, SpiceDBProtoClient } from "../client.js";

describe("createSpiceDBClient", () => {
  it("returns a client with all service properties", () => {
    const client = createSpiceDBClient("localhost:50051", "test-token");

    expect(client).toBeInstanceOf(SpiceDBProtoClient);
    expect(client.permissions).toBeDefined();
    expect(client.schema).toBeDefined();
    expect(client.watch).toBeDefined();
    expect(client.experimental).toBeDefined();
  });

  it("applies custom options", () => {
    const client = createSpiceDBClient("localhost:50051", "test-token", {
      insecure: true,
      headers: { "x-custom": "value" },
    });

    expect(client).toBeInstanceOf(SpiceDBProtoClient);
    expect(client.permissions).toBeDefined();
  });
});
