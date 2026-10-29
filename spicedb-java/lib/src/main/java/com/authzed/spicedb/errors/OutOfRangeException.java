package com.authzed.spicedb.errors;

/**
 * A ZedToken names a revision that is no longer available.
 *
 * <p>SpiceDB returns {@code OUT_OF_RANGE} when the revision a ZedToken refers to has expired or
 * been garbage-collected. Recovery is mechanical: discard the stale token and re-read at full
 * consistency.
 */
public class OutOfRangeException extends SpiceDBException {

  public OutOfRangeException(String message) {
    super(io.grpc.Status.Code.OUT_OF_RANGE, message, null);
  }

  public OutOfRangeException(String message, Throwable cause) {
    super(io.grpc.Status.Code.OUT_OF_RANGE, message, cause);
  }
}
