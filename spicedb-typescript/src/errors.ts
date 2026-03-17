import { ConnectError, Code } from "@connectrpc/connect";

/**
 * Base error class for all SpiceDB errors.
 */
export class SpiceDBError extends Error {
  constructor(message: string, options?: ErrorOptions) {
    super(message, options);
    this.name = "SpiceDBError";
  }
}

/**
 * The caller does not have permission to perform the operation.
 */
export class PermissionDeniedError extends SpiceDBError {
  constructor(message: string, options?: ErrorOptions) {
    super(message, options);
    this.name = "PermissionDeniedError";
  }
}

/**
 * The requested resource was not found.
 */
export class NotFoundError extends SpiceDBError {
  constructor(message: string, options?: ErrorOptions) {
    super(message, options);
    this.name = "NotFoundError";
  }
}

/**
 * The resource already exists.
 */
export class AlreadyExistsError extends SpiceDBError {
  constructor(message: string, options?: ErrorOptions) {
    super(message, options);
    this.name = "AlreadyExistsError";
  }
}

/**
 * An invalid argument was provided.
 */
export class InvalidArgumentError extends SpiceDBError {
  constructor(message: string, options?: ErrorOptions) {
    super(message, options);
    this.name = "InvalidArgumentError";
  }
}

/**
 * The operation was cancelled.
 */
export class CancelledError extends SpiceDBError {
  constructor(message: string, options?: ErrorOptions) {
    super(message, options);
    this.name = "CancelledError";
  }
}

/**
 * A precondition for the operation was not met.
 */
export class FailedPreconditionError extends SpiceDBError {
  constructor(message: string, options?: ErrorOptions) {
    super(message, options);
    this.name = "FailedPreconditionError";
  }
}

/**
 * The operation is not available (transient).
 */
export class UnavailableError extends SpiceDBError {
  constructor(message: string, options?: ErrorOptions) {
    super(message, options);
    this.name = "UnavailableError";
  }
}

const TRANSIENT_CODES = new Set([
  Code.Unavailable,
  Code.ResourceExhausted,
  Code.Aborted,
]);

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
 * Converts a ConnectError to a typed SpiceDB error.
 */
export function toSpiceDBError(err: unknown): SpiceDBError {
  if (err instanceof SpiceDBError) {
    return err;
  }

  if (err instanceof ConnectError) {
    const msg = err.message;
    switch (err.code) {
      case Code.PermissionDenied:
        return new PermissionDeniedError(msg, { cause: err });
      case Code.NotFound:
        return new NotFoundError(msg, { cause: err });
      case Code.AlreadyExists:
        return new AlreadyExistsError(msg, { cause: err });
      case Code.InvalidArgument:
        return new InvalidArgumentError(msg, { cause: err });
      case Code.Canceled:
        return new CancelledError(msg, { cause: err });
      case Code.FailedPrecondition:
        return new FailedPreconditionError(msg, { cause: err });
      case Code.Unavailable:
      case Code.ResourceExhausted:
      case Code.Aborted:
        return new UnavailableError(msg, { cause: err });
      default:
        return new SpiceDBError(msg, { cause: err });
    }
  }

  if (err instanceof Error) {
    return new SpiceDBError(err.message, { cause: err });
  }

  return new SpiceDBError(String(err));
}
