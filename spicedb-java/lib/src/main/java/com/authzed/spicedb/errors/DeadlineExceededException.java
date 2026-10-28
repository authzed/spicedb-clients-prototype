package com.authzed.spicedb.errors;

/** The operation deadline was exceeded before it could complete. */
public class DeadlineExceededException extends SpiceDBException {

  public DeadlineExceededException(String message) {
    super(io.grpc.Status.Code.DEADLINE_EXCEEDED, message, null);
  }

  public DeadlineExceededException(String message, Throwable cause) {
    super(io.grpc.Status.Code.DEADLINE_EXCEEDED, message, cause);
  }
}
