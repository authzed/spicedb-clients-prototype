package com.authzed.spicedb.errors;

/** The operation was cancelled, typically by the caller. */
public class CancelledException extends SpiceDBException {

  public CancelledException(String message) {
    super(message);
  }

  public CancelledException(String message, Throwable cause) {
    super(message, cause);
  }
}
