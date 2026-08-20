import { ConnectError, Code } from "@connectrpc/connect";
import type { Any } from "@bufbuild/protobuf/wkt";
import { anyUnpack } from "@bufbuild/protobuf/wkt";
import { ErrorInfoSchema } from "@spicedb/proto";

/**
 * Options accepted by every SpiceDB error, extending the standard
 * `ErrorOptions` (so `{ cause }` keeps working) with SpiceDB's structured
 * explanation of the failure.
 */
export interface SpiceDBErrorOptions extends ErrorOptions {
  /** See {@link SpiceDBError.reason}. */
  reason?: string;
  /** See {@link SpiceDBError.reasonDomain}. */
  reasonDomain?: string;
  /** See {@link SpiceDBError.reasonMetadata}. */
  reasonMetadata?: Record<string, string>;
}

/**
 * Base error class for all SpiceDB errors.
 *
 * Beyond the message, a SpiceDB error carries the server's structured
 * explanation of the failure when the server sent one -- the
 * `google.rpc.ErrorInfo` detail attached to the status. See root DESIGN.md,
 * "Error mapping must not lose the server's detail".
 */
export class SpiceDBError extends Error {
  /**
   * The name of an `authzed.api.v1.ErrorReason` enum value, e.g.
   * `"ERROR_REASON_MAXIMUM_DEPTH_EXCEEDED"`. Empty string when the server
   * attached no `ErrorInfo`.
   *
   * The value is surfaced exactly as the server sent it. A reason a newer
   * server knows and this client does not is passed through unchanged rather
   * than coerced or rejected: it is server-supplied, and root DESIGN.md's
   * "A conversion that cannot preserve meaning must fail" requires
   * server-supplied unknowns to degrade safely rather than throw.
   */
  readonly reason: string;

  /** Who produced the reason. SpiceDB uses `"authzed.com"`. */
  readonly reasonDomain: string;

  /**
   * The specifics behind the reason -- which precondition failed, what depth
   * limit was hit. Empty object when the server attached no `ErrorInfo`.
   */
  readonly reasonMetadata: Record<string, string>;

  constructor(message: string, options?: SpiceDBErrorOptions) {
    super(message, options);
    this.name = "SpiceDBError";
    this.reason = options?.reason ?? "";
    this.reasonDomain = options?.reasonDomain ?? "";
    this.reasonMetadata = options?.reasonMetadata ?? {};
  }
}

/**
 * The caller does not have permission to perform the operation.
 */
export class PermissionDeniedError extends SpiceDBError {
  constructor(message: string, options?: SpiceDBErrorOptions) {
    super(message, options);
    this.name = "PermissionDeniedError";
  }
}

/**
 * The requested resource was not found.
 */
export class NotFoundError extends SpiceDBError {
  constructor(message: string, options?: SpiceDBErrorOptions) {
    super(message, options);
    this.name = "NotFoundError";
  }
}

/**
 * The resource already exists.
 */
export class AlreadyExistsError extends SpiceDBError {
  constructor(message: string, options?: SpiceDBErrorOptions) {
    super(message, options);
    this.name = "AlreadyExistsError";
  }
}

/**
 * An invalid argument was provided.
 */
export class InvalidArgumentError extends SpiceDBError {
  constructor(message: string, options?: SpiceDBErrorOptions) {
    super(message, options);
    this.name = "InvalidArgumentError";
  }
}

/**
 * The operation was cancelled.
 */
export class CancelledError extends SpiceDBError {
  constructor(message: string, options?: SpiceDBErrorOptions) {
    super(message, options);
    this.name = "CancelledError";
  }
}

/**
 * A precondition for the operation was not met.
 */
export class FailedPreconditionError extends SpiceDBError {
  constructor(message: string, options?: SpiceDBErrorOptions) {
    super(message, options);
    this.name = "FailedPreconditionError";
  }
}

/**
 * The operation is not available (transient).
 */
export class UnavailableError extends SpiceDBError {
  constructor(message: string, options?: SpiceDBErrorOptions) {
    super(message, options);
    this.name = "UnavailableError";
  }
}

/**
 * The operation deadline was exceeded before it could complete.
 */
export class DeadlineExceededError extends SpiceDBError {
  constructor(message: string, options?: SpiceDBErrorOptions) {
    super(message, options);
    this.name = "DeadlineExceededError";
  }
}

/**
 * A resource quota or limit was exhausted, such as a rate limit.
 */
export class ResourceExhaustedError extends SpiceDBError {
  constructor(message: string, options?: SpiceDBErrorOptions) {
    super(message, options);
    this.name = "ResourceExhaustedError";
  }
}

/**
 * The request carried no usable credentials.
 *
 * In SpiceDB this is a wrong, expired, or rotated API token -- the most common
 * error a new integration produces. It is distinct from
 * {@link PermissionDeniedError}, which means the caller was identified but is
 * not allowed, and from a bare {@link SpiceDBError}, which may be an internal
 * server fault: refresh credentials on this one, page someone on that one.
 */
export class UnauthenticatedError extends SpiceDBError {
  constructor(message: string, options?: SpiceDBErrorOptions) {
    super(message, options);
    this.name = "UnauthenticatedError";
  }
}

/**
 * A ZedToken names a revision that is no longer available.
 *
 * SpiceDB returns `OUT_OF_RANGE` when the revision a ZedToken refers to has
 * expired or been garbage-collected. Recovery is mechanical: discard the stale
 * token and re-read at full consistency.
 */
export class OutOfRangeError extends SpiceDBError {
  constructor(message: string, options?: SpiceDBErrorOptions) {
    super(message, options);
    this.name = "OutOfRangeError";
  }
}

// ResourceExhausted is deliberately excluded. In SpiceDB it signals either
// memory load-shed (retrying adds load to an already-overloaded server) or a
// deterministic MaxDepthExceeded (retrying can never succeed -- it just
// re-runs the most expensive class of check several times before surfacing
// the same error). See DESIGN.md, "Automatic retry is for idempotent
// operations only".
const TRANSIENT_CODES = new Set([Code.Unavailable, Code.Aborted]);

/**
 * Returns true if the error is transient and should be retried.
 */
export function isTransientError(err: unknown): boolean {
  if (err instanceof ConnectError) {
    return TRANSIENT_CODES.has(err.code);
  }
  return false;
}

/**
 * Reads SpiceDB's `google.rpc.ErrorInfo` detail off a {@link ConnectError} and
 * turns it into constructor options. `findDetails` does the decoding, so this
 * works against the structured detail the transport parsed out of
 * `grpc-status-details-bin` rather than against anything reconstructed from
 * the error's message. Details it cannot decode are dropped by `findDetails`
 * itself, so a malformed or unfamiliar detail never costs the caller the
 * code-to-type mapping.
 */
function reasonOptionsFromConnectError(err: ConnectError): SpiceDBErrorOptions {
  const [info] = err.findDetails(ErrorInfoSchema);
  if (info === undefined) {
    return {};
  }
  return {
    reason: info.reason,
    reasonDomain: info.domain,
    reasonMetadata: { ...info.metadata },
  };
}

/**
 * Converts a ConnectError to a typed SpiceDB error.
 *
 * The originating error is preserved as `cause`, and SpiceDB's structured
 * reason -- when the server sent one -- is surfaced on the returned error's
 * `reason`/`reasonDomain`/`reasonMetadata`. See root DESIGN.md, "Error mapping
 * must not lose the server's detail".
 */
export function toSpiceDBError(err: unknown): SpiceDBError {
  if (err instanceof SpiceDBError) {
    return err;
  }

  if (err instanceof ConnectError) {
    return mapConnectError(err, reasonOptionsFromConnectError(err));
  }

  if (err instanceof Error) {
    return new SpiceDBError(err.message, { cause: err });
  }

  return new SpiceDBError(String(err));
}

/**
 * Maps a {@link ConnectError}'s code to a typed error class, attaching the
 * error as `cause` and whatever structured reason the caller resolved.
 */
function mapConnectError(
  err: ConnectError,
  reason: SpiceDBErrorOptions,
): SpiceDBError {
  const msg = err.message;
  const opts: SpiceDBErrorOptions = { cause: err, ...reason };
  switch (err.code) {
    case Code.PermissionDenied:
      return new PermissionDeniedError(msg, opts);
    case Code.NotFound:
      return new NotFoundError(msg, opts);
    case Code.AlreadyExists:
      return new AlreadyExistsError(msg, opts);
    case Code.InvalidArgument:
      return new InvalidArgumentError(msg, opts);
    case Code.Canceled:
      return new CancelledError(msg, opts);
    case Code.FailedPrecondition:
      return new FailedPreconditionError(msg, opts);
    case Code.DeadlineExceeded:
      return new DeadlineExceededError(msg, opts);
    case Code.ResourceExhausted:
      return new ResourceExhaustedError(msg, opts);
    case Code.Unauthenticated:
      return new UnauthenticatedError(msg, opts);
    case Code.OutOfRange:
      return new OutOfRangeError(msg, opts);
    case Code.Unavailable:
    case Code.Aborted:
      return new UnavailableError(msg, opts);
    default:
      return new SpiceDBError(msg, opts);
  }
}

/**
 * Converts a per-item `google.rpc.Status` (the shape carried by
 * `CheckBulkPermissionsPair.error`, and any other per-item bulk-response
 * error) to a typed {@link SpiceDBError}, reusing {@link toSpiceDBError}'s
 * code -> error-class mapping.
 *
 * `google.rpc.Code`'s numeric values are identical to Connect's {@link Code}
 * enum — both mirror the standard gRPC status codes — so the status's
 * `code` can be passed straight through to `ConnectError`'s constructor
 * without a separate mapping table.
 *
 * A per-item error from a bulk RPC (e.g. `CheckBulkPermissions`) MUST be
 * routed through here rather than silently coerced into a falsy result —
 * a permission-denied, an invalid-argument, and an internal server error
 * are meaningfully different outcomes for a caller and must not be
 * indistinguishable.
 *
 * A per-item status carries its own `details`, which are unpacked here with
 * protobuf's own `anyUnpack` so a per-item failure surfaces the same structured
 * `reason` and metadata an RPC-level failure does.
 */
export function toSpiceDBErrorFromStatus(status: {
  code: number;
  message: string;
  details?: Any[];
}): SpiceDBError {
  let reason: SpiceDBErrorOptions = {};
  for (const detail of status.details ?? []) {
    const info = anyUnpack(detail, ErrorInfoSchema);
    if (info !== undefined) {
      reason = {
        reason: info.reason,
        reasonDomain: info.domain,
        reasonMetadata: { ...info.metadata },
      };
      break;
    }
  }
  return mapConnectError(
    new ConnectError(status.message, status.code as Code),
    reason,
  );
}
