package com.authzed.spicedb.errors;

/** The operation was cancelled, typically by the caller. */
public class CancelledException extends SpiceDBException {

  public CancelledException(String message) {
    super(io.grpc.Status.Code.CANCELLED, message, null);
  }

  public CancelledException(String message, Throwable cause) {
    super(io.grpc.Status.Code.CANCELLED, message, cause);
  }
}
