import { describe, it, expect } from "vitest";
import { ConnectError, Code } from "@connectrpc/connect";
import { create, toBinary } from "@bufbuild/protobuf";
import { anyPack } from "@bufbuild/protobuf/wkt";
import { ErrorInfoSchema, ErrorReason, ErrorReasonSchema } from "@spicedb/proto";
import {
  SpiceDBError,
  PermissionDeniedError,
  NotFoundError,
  AlreadyExistsError,
  InvalidArgumentError,
  UnavailableError,
  UnauthenticatedError,
  OutOfRangeError,
  FailedPreconditionError,
  CancelledError,
  DeadlineExceededError,
  ResourceExhaustedError,
  toSpiceDBError,
  toSpiceDBErrorFromStatus,
  isTransientError,
} from "../errors.js";

/**
 * Builds a ConnectError carrying a `google.rpc.ErrorInfo` detail in the shape
 * the gRPC transport produces for one: an incoming detail is the type name
 * plus the serialized message, not a schema/value pair.
 */
function connectErrorWithReason(
  code: Code,
  message: string,
  reason: string,
  metadata: Record<string, string>,
  domain = "authzed.com",
): ConnectError {
  const err = new ConnectError(message, code);
  err.details = [
    {
      type: ErrorInfoSchema.typeName,
      value: toBinary(
        ErrorInfoSchema,
        create(ErrorInfoSchema, { reason, domain, metadata }),
      ),
    },
  ];
  return err;
}

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

  // OUT_OF_RANGE is SpiceDB's code for an expired or garbage-collected
  // ZedToken. Recovery is mechanical -- drop the token, re-read at full
  // consistency -- so it has to be distinguishable by type rather than by
  // message.
  it("maps OutOfRange to its own type", () => {
    const err = new ConnectError("revision no longer available", Code.OutOfRange);
    const result = toSpiceDBError(err);
    expect(result).toBeInstanceOf(OutOfRangeError);
    expect(result).toBeInstanceOf(SpiceDBError);
    expect(result).not.toBeInstanceOf(InvalidArgumentError);
    expect(result.name).toBe("OutOfRangeError");
  });

  // A wrong, expired, or rotated token must be distinguishable from an
  // internal server fault, so a caller can refresh credentials on one and page
  // someone on the other.
  it("maps Unauthenticated to its own type", () => {
    const err = new ConnectError("bad token", Code.Unauthenticated);
    const result = toSpiceDBError(err);
    expect(result).toBeInstanceOf(UnauthenticatedError);
    expect(result).not.toBeInstanceOf(PermissionDeniedError);
    expect(result.name).toBe("UnauthenticatedError");
  });

  it("neither newly mapped code is retried", () => {
    expect(isTransientError(new ConnectError("x", Code.OutOfRange))).toBe(false);
    expect(isTransientError(new ConnectError("x", Code.Unauthenticated))).toBe(
      false,
    );
  });
});

// SpiceDB's structured explanation of a failure -- the google.rpc.ErrorInfo
// detail on the status -- must survive the mapping into a typed error, so a
// caller can branch on the reason and read its metadata instead of
// string-matching a message. See root DESIGN.md, "Error mapping must not lose
// the server's detail".
describe("ErrorReason is reachable", () => {
  it("surfaces the reason, domain, and metadata", () => {
    const err = connectErrorWithReason(
      Code.ResourceExhausted,
      "max depth exceeded",
      "ERROR_REASON_MAXIMUM_DEPTH_EXCEEDED",
      { maximum_depth_allowed: "50" },
    );
    const result = toSpiceDBError(err);

    expect(result).toBeInstanceOf(ResourceExhaustedError);
    // The exposed reason is exactly the authzed.api.v1.ErrorReason enum name,
    // so a caller can compare against the generated enum without this client
    // carrying a hand-maintained copy of it.
    expect(result.reason).toBe("ERROR_REASON_MAXIMUM_DEPTH_EXCEEDED");
    expect(result.reason).toBe(
      ErrorReasonSchema.value[ErrorReason.MAXIMUM_DEPTH_EXCEEDED]?.name,
    );
    expect(result.reasonDomain).toBe("authzed.com");
    expect(result.reasonMetadata).toEqual({ maximum_depth_allowed: "50" });
  });

  it("keeps the metadata naming which precondition failed", () => {
    const err = connectErrorWithReason(
      Code.FailedPrecondition,
      "precondition failed",
      "ERROR_REASON_WRITE_OR_DELETE_PRECONDITION_FAILURE",
      {
        precondition_resource_id: "firstdoc",
        precondition_relation: "viewer",
      },
    );
    const result = toSpiceDBError(err);

    expect(result).toBeInstanceOf(FailedPreconditionError);
    expect(result.reason).toBe(
      "ERROR_REASON_WRITE_OR_DELETE_PRECONDITION_FAILURE",
    );
    expect(result.reasonMetadata.precondition_resource_id).toBe("firstdoc");
    expect(result.reasonMetadata.precondition_relation).toBe("viewer");
  });

  // A reason a newer server knows and this client does not is server-supplied:
  // root DESIGN.md's "A conversion that cannot preserve meaning must fail"
  // requires it to degrade safely, not to throw.
  it("passes an unrecognized reason through without throwing", () => {
    const err = connectErrorWithReason(
      Code.InvalidArgument,
      "from the future",
      "ERROR_REASON_INVENTED_BY_A_NEWER_SERVER",
      { k: "v" },
    );
    const result = toSpiceDBError(err);

    expect(result).toBeInstanceOf(InvalidArgumentError);
    expect(result.reason).toBe("ERROR_REASON_INVENTED_BY_A_NEWER_SERVER");
    expect(result.reasonMetadata).toEqual({ k: "v" });
  });

  it("leaves the reason empty when the server attached no ErrorInfo", () => {
    const result = toSpiceDBError(new ConnectError("nope", Code.NotFound));
    expect(result.reason).toBe("");
    expect(result.reasonDomain).toBe("");
    expect(result.reasonMetadata).toEqual({});
  });

  // A per-item bulk error arrives as a google.rpc.Status with its own details,
  // and must not lose them on the way to a typed error.
  it("surfaces the reason from a per-item google.rpc.Status", () => {
    const result = toSpiceDBErrorFromStatus({
      code: 8,
      message: "max depth exceeded",
      details: [
        anyPack(
          ErrorInfoSchema,
          create(ErrorInfoSchema, {
            reason: "ERROR_REASON_MAXIMUM_DEPTH_EXCEEDED",
            domain: "authzed.com",
            metadata: { maximum_depth_allowed: "50" },
          }),
        ),
      ],
    });

    expect(result).toBeInstanceOf(ResourceExhaustedError);
    expect(result.reason).toBe("ERROR_REASON_MAXIMUM_DEPTH_EXCEEDED");
    expect(result.reasonMetadata).toEqual({ maximum_depth_allowed: "50" });
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
