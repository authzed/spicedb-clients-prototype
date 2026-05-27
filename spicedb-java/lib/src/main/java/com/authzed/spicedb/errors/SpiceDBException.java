package com.authzed.spicedb.errors;

/**
 * Base unchecked exception for all SpiceDB errors.
 *
 * <p>All SpiceDB-specific exceptions extend this class, allowing callers to catch all SpiceDB
 * errors with a single {@code catch (SpiceDBException e)}.
 */
public class SpiceDBException extends RuntimeException {

  public SpiceDBException(String message) {
    super(message);
  }

  public SpiceDBException(String message, Throwable cause) {
    super(message, cause);
  }
}
