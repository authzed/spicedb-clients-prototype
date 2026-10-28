package com.authzed.spicedb.errors;

/** The requested resource was not found. */
public class NotFoundException extends SpiceDBException {

  public NotFoundException(String message) {
    super(io.grpc.Status.Code.NOT_FOUND, message, null);
  }

  public NotFoundException(String message, Throwable cause) {
    super(io.grpc.Status.Code.NOT_FOUND, message, cause);
  }
}
