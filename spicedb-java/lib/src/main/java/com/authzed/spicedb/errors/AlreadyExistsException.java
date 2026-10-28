package com.authzed.spicedb.errors;

/** The resource already exists. */
public class AlreadyExistsException extends SpiceDBException {

  public AlreadyExistsException(String message) {
    super(io.grpc.Status.Code.ALREADY_EXISTS, message, null);
  }

  public AlreadyExistsException(String message, Throwable cause) {
    super(io.grpc.Status.Code.ALREADY_EXISTS, message, cause);
  }
}
