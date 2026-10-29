package com.authzed.spicedb.errors;

/** The service is currently unavailable. */
public class UnavailableException extends SpiceDBException {

  public UnavailableException(String message) {
    super(io.grpc.Status.Code.UNAVAILABLE, message, null);
  }

  public UnavailableException(String message, Throwable cause) {
    super(io.grpc.Status.Code.UNAVAILABLE, message, cause);
  }
}
