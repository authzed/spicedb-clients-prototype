import { ConnectError, Code } from "@connectrpc/connect";
import type { Any } from "@bufbuild/protobuf/wkt";
import { anyUnpack } from "@bufbuild/protobuf/wkt";
import type { DeleteResult } from "./types.js";
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
  reasonMetadata?: Readonly<Record<string, string>>;
  /**
   * See {@link SpiceDBError.code}. Each subclass has a default; pass this
   * only to record a more specific transport code (e.g. `Aborted` on an
   * {@link UnavailableError}).
   */
  code?: Code;
  /** See {@link SpiceDBError.serverMessage}. */
  serverMessage?: string;
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
  declare readonly deleteProgress?: DeleteResult;
  declare readonly inputIndex?: number;
  /**
   * Set on an error thrown while iterating a cursor-paged read
   * (`readRelationships`, `lookupResources`, `exportRelationships`): the
   * cursor to resume from. Pass it as the request's `cursor`, with the same
   * request and page size, to continue after the last item the read
   * delivered (for an export, after the last fully delivered batch). It is
   * the request's own cursor when nothing was delivered yet, and absent when
   * the read should restart from the beginning.
   */
  declare readonly resumeCursor?: string;
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
  readonly reasonMetadata: Readonly<Record<string, string>>;

  /**
   * The Connect (gRPC) status code this error corresponds to, using the same
   * `Code` enum `ConnectError.code` uses -- so `err.code === Code.NotFound`
   * reads the same on a native error and on a raw `ConnectError`. It is set
   * for every native error: server errors carry the server's code, local
   * validation is `InvalidArgument`, a client-side deadline is
   * `DeadlineExceeded`, and errors built by caller code (see
   * {@link SpiceDBError.fromCode}) carry the code they were built with.
   */
  readonly code: Code;

  /**
   * The server's status message, verbatim, when this error came from the
   * server. `message` may add operation context; callers that forward errors
   * reproduce exactly what the server said from this field. Undefined for
   * locally raised errors.
   */
  readonly serverMessage?: string;

  constructor(message: string, options?: SpiceDBErrorOptions) {
    super(message, options);
    this.name = "SpiceDBError";
    this.reason = options?.reason ?? "";
    this.reasonDomain = options?.reasonDomain ?? "";
    this.reasonMetadata = Object.freeze({ ...options?.reasonMetadata });
    this.code = options?.code ?? (new.target as unknown as { defaultCode?: Code }).defaultCode ?? Code.Unknown;
    if (options?.serverMessage !== undefined) this.serverMessage = options.serverMessage;
  }

  /** @internal The code a subclass carries when none is supplied. */
  static readonly defaultCode: Code = Code.Unknown;

  /**
   * Builds the native error for `code`: the same subclass, code and predicate
   * behavior a server error with that code produces. Intended for test
   * doubles and fakes, so that `instanceof`, {@link errorCode} and
   * {@link SpiceDBError.toConnectError} treat their errors like real ones.
   */
  static fromCode(code: Code, message: string, options?: Omit<SpiceDBErrorOptions, "code">): SpiceDBError {
    return mapCode(code, message, { ...options, code });
  }

  /**
   * Converts any thrown value -- a raw `ConnectError` from a protocol client
   * or a fake, an abort, or a native error -- into a native error. Native
   * errors are returned unchanged.
   */
  static from(error: unknown): SpiceDBError {
    return toSpiceDBError(error);
  }

  /**
   * Returns the `ConnectError` form of this error, for code that follows the
   * Connect convention (`ConnectError.from(err).code`). A server error
   * returns the server's original `ConnectError` (metadata and details
   * included); any other error returns a new `ConnectError` with this
   * error's {@link SpiceDBError.code}, message and this error as `cause`.
   *
   * `ConnectError.from(nativeError)` cannot do this itself: ConnectError's
   * `Symbol.hasInstance` recognizes only its own prototype or the name
   * "ConnectError", so no subclass with its own name passes
   * `instanceof ConnectError`. Use this method, {@link toConnectError} or
   * {@link errorCode} instead.
   */
  toConnectError(): ConnectError {
    if (this.cause instanceof ConnectError && this.cause.code === this.code)
      return this.cause;
    return new ConnectError(this.serverMessage ?? this.message, this.code, undefined, undefined, this);
  }
}

/**
 * The caller does not have permission to perform the operation.
 */
export class PermissionDeniedError extends SpiceDBError {
  /** @internal */ static override readonly defaultCode: Code = Code.PermissionDenied;
  constructor(message: string, options?: SpiceDBErrorOptions) {
    super(message, options);
    this.name = "PermissionDeniedError";
  }
}

/**
 * The requested resource was not found.
 */
export class NotFoundError extends SpiceDBError {
  /** @internal */ static override readonly defaultCode: Code = Code.NotFound;
  constructor(message: string, options?: SpiceDBErrorOptions) {
    super(message, options);
    this.name = "NotFoundError";
  }
}

/**
 * The resource already exists.
 */
export class AlreadyExistsError extends SpiceDBError {
  /** @internal */ static override readonly defaultCode: Code = Code.AlreadyExists;
  constructor(message: string, options?: SpiceDBErrorOptions) {
    super(message, options);
    this.name = "AlreadyExistsError";
  }
}

/**
 * An invalid argument was provided.
 */
export class InvalidArgumentError extends SpiceDBError {
  /** @internal */ static override readonly defaultCode: Code = Code.InvalidArgument;
  constructor(message: string, options?: SpiceDBErrorOptions) {
    super(message, options);
    this.name = "InvalidArgumentError";
  }
}

/**
 * The operation was cancelled.
 */
export class CancelledError extends SpiceDBError {
  /** @internal */ static override readonly defaultCode: Code = Code.Canceled;
  constructor(message: string, options?: SpiceDBErrorOptions) {
    super(message, options);
    this.name = "CancelledError";
  }
}

/**
 * A precondition for the operation was not met.
 */
export class FailedPreconditionError extends SpiceDBError {
  /** @internal */ static override readonly defaultCode: Code = Code.FailedPrecondition;
  constructor(message: string, options?: SpiceDBErrorOptions) {
    super(message, options);
    this.name = "FailedPreconditionError";
  }
}

/**
 * The operation is not available (transient).
 */
export class UnavailableError extends SpiceDBError {
  /** @internal */ static override readonly defaultCode: Code = Code.Unavailable;
  constructor(message: string, options?: SpiceDBErrorOptions) {
    super(message, options);
    this.name = "UnavailableError";
  }
}

/**
 * The operation deadline was exceeded before it could complete.
 */
export class DeadlineExceededError extends SpiceDBError {
  /** @internal */ static override readonly defaultCode: Code = Code.DeadlineExceeded;
  constructor(message: string, options?: SpiceDBErrorOptions) {
    super(message, options);
    this.name = "DeadlineExceededError";
  }
}

/**
 * A resource quota or limit was exhausted, such as a rate limit.
 */
export class ResourceExhaustedError extends SpiceDBError {
  /** @internal */ static override readonly defaultCode: Code = Code.ResourceExhausted;
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
  /** @internal */ static override readonly defaultCode: Code = Code.Unauthenticated;
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
  /** @internal */ static override readonly defaultCode: Code = Code.OutOfRange;
  constructor(message: string, options?: SpiceDBErrorOptions) {
    super(message, options);
    this.name = "OutOfRangeError";
  }
}

/**
 * Classifies any thrown value by its Connect (gRPC) status code.
 *
 * Accepts native errors (their {@link SpiceDBError.code}), raw `ConnectError`s
 * from the protocol client or a test double, and the platform's abort and
 * timeout `DOMException`s (`Canceled` and `DeadlineExceeded`). Anything else
 * is `Unknown`. This is the native counterpart of
 * `ConnectError.from(err).code`, which reports `Unknown` for a native error
 * (see {@link SpiceDBError.toConnectError}).
 */
export function errorCode(err: unknown): Code {
  if (err instanceof SpiceDBError || err instanceof ConnectError) return err.code;
  if (err instanceof Error && err.name === "AbortError") return Code.Canceled;
  if (err instanceof Error && err.name === "TimeoutError") return Code.DeadlineExceeded;
  return Code.Unknown;
}

/**
 * Converts any thrown value to a `ConnectError` whose `code` matches
 * {@link errorCode}. A native server error returns the server's original
 * `ConnectError`. Use this where code written for the Connect convention
 * would call `ConnectError.from(err)`.
 */
export function toConnectError(err: unknown): ConnectError {
  if (err instanceof SpiceDBError) return err.toConnectError();
  if (err instanceof ConnectError) return err;
  const code = errorCode(err);
  if (err instanceof Error) return new ConnectError(err.message, code, undefined, undefined, err);
  return ConnectError.from(err);
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
 * The originating error is preserved as `cause`, its code as `code`, its
 * unprefixed message as `serverMessage`, and SpiceDB's structured reason --
 * when the server sent one -- on `reason`/`reasonDomain`/`reasonMetadata`.
 * See root DESIGN.md, "Expose native errors without losing server diagnostics".
 * @internal
 */
export function toSpiceDBError(err: unknown): SpiceDBError {
  if (err instanceof SpiceDBError) {
    return err;
  }

  if (err instanceof ConnectError) {
    return mapCode(err.code, err.message, { cause: err, serverMessage: err.rawMessage, ...reasonOptionsFromConnectError(err) });
  }

  if (err instanceof Error) {
    return mapCode(errorCode(err), err.message, { cause: err });
  }

  return new SpiceDBError(String(err), { cause: err });
}

/**
 * Maps a status code to the typed error class, attaching the code and
 * whatever options the caller resolved.
 */
function mapCode(code: Code, msg: string, options: SpiceDBErrorOptions): SpiceDBError {
  const opts: SpiceDBErrorOptions = { ...options, code };
  switch (code) {
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
 * `CheckBulkPermissionsPair.error`) to a typed {@link SpiceDBError}, reusing
 * the code -> error-class mapping. `google.rpc.Code`'s numeric values are
 * identical to Connect's {@link Code} enum. The per-item status's details
 * are unpacked so a per-item failure surfaces the same structured `reason`
 * an RPC-level failure does, and its message is kept verbatim as
 * `serverMessage`.
 * @internal
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
  const code = status.code as Code;
  return mapCode(code, new ConnectError(status.message, code).message, { ...reason, cause: status, serverMessage: status.message });
}

/**
 * The error for a response the client cannot interpret: a missing required
 * field, a cursor that does not advance, a malformed bulk pair. It is coded
 * `Internal`, which no retry policy treats as retryable by default.
 * @internal
 */
export function malformedResponse(message: string): SpiceDBError {
  return new SpiceDBError(message, { code: Code.Internal });
}

// ResourceExhausted is deliberately excluded from the default. In SpiceDB it
// signals either memory load-shed (retrying adds load to an already-overloaded
// server) or a deterministic MaxDepthExceeded (retrying can never succeed).
/** @internal The default retry policy's codes. */
export const DEFAULT_RETRYABLE_CODES: readonly Code[] = Object.freeze([Code.Unavailable, Code.Aborted]);

/** @internal Whether the default retry policy would retry this transport error. */
export function isTransientError(err: unknown): boolean {
  return err instanceof ConnectError && DEFAULT_RETRYABLE_CODES.includes(err.code);
}
