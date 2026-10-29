package com.authzed.spicedb.errors;

/** The operation was rejected because the system is not in a required state. */
public class FailedPreconditionException extends SpiceDBException {

  public FailedPreconditionException(String message) {
    super(io.grpc.Status.Code.FAILED_PRECONDITION, message, null);
  }

  public FailedPreconditionException(String message, Throwable cause) {
    super(io.grpc.Status.Code.FAILED_PRECONDITION, message, cause);
  }
}
