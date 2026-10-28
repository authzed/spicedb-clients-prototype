package com.authzed.spicedb.errors;

/** The request contained an invalid argument. */
public class InvalidArgumentException extends SpiceDBException {

  public InvalidArgumentException(String message) {
    super(io.grpc.Status.Code.INVALID_ARGUMENT, message, null);
  }

  public InvalidArgumentException(String message, Throwable cause) {
    super(io.grpc.Status.Code.INVALID_ARGUMENT, message, cause);
  }
}
