package com.authzed.spicedb.errors;

/** A resource quota or limit was exhausted, such as a rate limit. */
public class ResourceExhaustedException extends SpiceDBException {

  public ResourceExhaustedException(String message) {
    super(message);
  }

  public ResourceExhaustedException(String message, Throwable cause) {
    super(message, cause);
  }
}
