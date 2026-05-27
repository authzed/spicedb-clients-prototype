package com.authzed.spicedb.errors;

/** The resource already exists. */
public class AlreadyExistsException extends SpiceDBException {

  public AlreadyExistsException(String message) {
    super(message);
  }

  public AlreadyExistsException(String message, Throwable cause) {
    super(message, cause);
  }
}
