package com.authzed.spicedb.errors;

/** The requested resource was not found. */
public class NotFoundException extends SpiceDBException {

  public NotFoundException(String message) {
    super(message);
  }

  public NotFoundException(String message, Throwable cause) {
    super(message, cause);
  }
}
