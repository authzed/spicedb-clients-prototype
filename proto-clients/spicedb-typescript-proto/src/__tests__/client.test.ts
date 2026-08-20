import { describe, it, expect, vi, afterEach } from "vitest";
import { Http2SessionManager } from "@connectrpc/connect-node";
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

describe("SpiceDBProtoClient#close", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("is idempotent -- calling it more than once does not throw", () => {
    const client = createSpiceDBClient("localhost:50051", "test-token", {
      insecure: true,
    });

    expect(() => client.close()).not.toThrow();
    expect(() => client.close()).not.toThrow();
    expect(() => client.close()).not.toThrow();
  });

  // Asserts on the transport itself -- not merely that close() runs without
  // throwing, which a no-op implementation would also satisfy. Spies on the
  // REAL Http2SessionManager.abort (the method that tears down the
  // underlying HTTP/2 connection -- see its own doc comment: "If there is
  // an open connection, close it. This also closes any open streams."),
  // not a fake stand-in, so this fails if close() stops calling it, is
  // wired to the wrong instance, or degrades to a no-op.
  it("aborts the underlying Http2SessionManager exactly once, even if close() is called twice", () => {
    const abortSpy = vi.spyOn(Http2SessionManager.prototype, "abort");

    const client = createSpiceDBClient("localhost:50051", "test-token", {
      insecure: true,
    });

    expect(abortSpy).not.toHaveBeenCalled();

    client.close();
    expect(abortSpy).toHaveBeenCalledTimes(1);

    client.close();
    expect(abortSpy).toHaveBeenCalledTimes(1);
  });
});
