package com.authzed.spicedb.errors;

/** The operation was rejected because the system is not in a required state. */
public class FailedPreconditionException extends SpiceDBException {

  public FailedPreconditionException(String message) {
    super(message);
  }

  public FailedPreconditionException(String message, Throwable cause) {
    super(message, cause);
  }
}
