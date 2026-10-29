package com.authzed.spicedb.errors;

/** A resource quota or limit was exhausted, such as a rate limit. */
public class ResourceExhaustedException extends SpiceDBException {

  public ResourceExhaustedException(String message) {
    super(io.grpc.Status.Code.RESOURCE_EXHAUSTED, message, null);
  }

  public ResourceExhaustedException(String message, Throwable cause) {
    super(io.grpc.Status.Code.RESOURCE_EXHAUSTED, message, cause);
  }
}
