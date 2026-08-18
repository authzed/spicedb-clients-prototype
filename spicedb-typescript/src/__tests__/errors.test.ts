import { describe, it, expect } from "vitest";
import { ConnectError, Code } from "@connectrpc/connect";
import {
  SpiceDBError,
  PermissionDeniedError,
  NotFoundError,
  AlreadyExistsError,
  InvalidArgumentError,
  UnavailableError,
  FailedPreconditionError,
  CancelledError,
  DeadlineExceededError,
  ResourceExhaustedError,
  toSpiceDBError,
  toSpiceDBErrorFromStatus,
  isTransientError,
} from "../errors.js";

describe("toSpiceDBError", () => {
  it("maps PermissionDenied", () => {
    const err = new ConnectError("denied", Code.PermissionDenied);
    const result = toSpiceDBError(err);
    expect(result).toBeInstanceOf(PermissionDeniedError);
    expect(result.cause).toBe(err);
  });

  it("maps NotFound", () => {
    const err = new ConnectError("not found", Code.NotFound);
    expect(toSpiceDBError(err)).toBeInstanceOf(NotFoundError);
  });

  it("maps AlreadyExists", () => {
    const err = new ConnectError("exists", Code.AlreadyExists);
    expect(toSpiceDBError(err)).toBeInstanceOf(AlreadyExistsError);
  });

  it("maps InvalidArgument", () => {
    const err = new ConnectError("bad", Code.InvalidArgument);
    expect(toSpiceDBError(err)).toBeInstanceOf(InvalidArgumentError);
  });

  it("maps FailedPrecondition", () => {
    const err = new ConnectError("precondition", Code.FailedPrecondition);
    expect(toSpiceDBError(err)).toBeInstanceOf(FailedPreconditionError);
  });

  it("maps Unavailable", () => {
    const err = new ConnectError("unavailable", Code.Unavailable);
    expect(toSpiceDBError(err)).toBeInstanceOf(UnavailableError);
  });

  it("maps Cancelled", () => {
    const err = new ConnectError("cancelled", Code.Canceled);
    expect(toSpiceDBError(err)).toBeInstanceOf(CancelledError);
  });

  it("maps DeadlineExceeded", () => {
    const err = new ConnectError("timeout", Code.DeadlineExceeded);
    expect(toSpiceDBError(err)).toBeInstanceOf(DeadlineExceededError);
  });

  it("maps ResourceExhausted", () => {
    const err = new ConnectError("quota", Code.ResourceExhausted);
    expect(toSpiceDBError(err)).toBeInstanceOf(ResourceExhaustedError);
  });

  it("maps unknown errors", () => {
    const err = new ConnectError("unknown", Code.Internal);
    expect(toSpiceDBError(err)).toBeInstanceOf(SpiceDBError);
  });

  it("wraps non-ConnectError", () => {
    const err = new Error("boom");
    const result = toSpiceDBError(err);
    expect(result).toBeInstanceOf(SpiceDBError);
    expect(result.message).toBe("boom");
  });

  it("wraps non-Error", () => {
    const result = toSpiceDBError("oops");
    expect(result).toBeInstanceOf(SpiceDBError);
    expect(result.message).toBe("oops");
  });

  it("passes through SpiceDBError", () => {
    const err = new NotFoundError("nope");
    expect(toSpiceDBError(err)).toBe(err);
  });
});

describe("toSpiceDBErrorFromStatus", () => {
  // Regression coverage for the CheckBulkPermissions per-item error path:
  // a google.rpc.Status-shaped per-item error (numeric code, not a
  // ConnectError instance) must map to the same typed error classes as a
  // top-level RPC failure — a caller must not be able to tell a per-item
  // error apart from an RPC-level one except by catching a typed error.
  it("maps PermissionDenied (code 7)", () => {
    const result = toSpiceDBErrorFromStatus({ code: 7, message: "nope" });
    expect(result).toBeInstanceOf(PermissionDeniedError);
  });

  it("maps InvalidArgument (code 3)", () => {
    const result = toSpiceDBErrorFromStatus({ code: 3, message: "bad" });
    expect(result).toBeInstanceOf(InvalidArgumentError);
  });

  it("maps an unrecognized/internal code to the base SpiceDBError", () => {
    const result = toSpiceDBErrorFromStatus({ code: 13, message: "boom" });
    expect(result).toBeInstanceOf(SpiceDBError);
  });

  it("carries the status message through", () => {
    const result = toSpiceDBErrorFromStatus({ code: 5, message: "no such thing" });
    expect(result).toBeInstanceOf(NotFoundError);
    expect(result.message).toContain("no such thing");
  });
});

describe("isTransientError", () => {
  it("returns true for Unavailable", () => {
    expect(
      isTransientError(new ConnectError("", Code.Unavailable)),
    ).toBe(true);
  });

  it("returns false for ResourceExhausted", () => {
    // Inverted from "returns true" -- RESOURCE_EXHAUSTED must NOT be
    // retried. In SpiceDB it signals memory load-shed (retrying adds load
    // to an already-overloaded server) or a deterministic MaxDepthExceeded
    // (retrying can never succeed). See DESIGN.md, "Automatic retry is for
    // idempotent operations only".
    expect(
      isTransientError(new ConnectError("", Code.ResourceExhausted)),
    ).toBe(false);
  });

  it("returns true for Aborted", () => {
    expect(
      isTransientError(new ConnectError("", Code.Aborted)),
    ).toBe(true);
  });

  it("returns false for PermissionDenied", () => {
    expect(
      isTransientError(new ConnectError("", Code.PermissionDenied)),
    ).toBe(false);
  });

  it("returns false for non-ConnectError", () => {
    expect(isTransientError(new Error("boom"))).toBe(false);
  });
});
