package com.authzed.spicedb.errors;

/** The operation deadline was exceeded before it could complete. */
public class DeadlineExceededException extends SpiceDBException {

  public DeadlineExceededException(String message) {
    super(message);
  }

  public DeadlineExceededException(String message, Throwable cause) {
    super(message, cause);
  }
}
