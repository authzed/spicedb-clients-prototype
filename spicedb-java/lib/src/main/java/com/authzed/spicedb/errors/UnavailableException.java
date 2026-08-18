package com.authzed.spicedb.errors;

/** The service is currently unavailable. */
public class UnavailableException extends SpiceDBException {

  public UnavailableException(String message) {
    super(message);
  }

  public UnavailableException(String message, Throwable cause) {
    super(message, cause);
  }
}
