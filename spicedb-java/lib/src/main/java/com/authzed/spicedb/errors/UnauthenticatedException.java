package com.authzed.spicedb.errors;

/**
 * The request carried no usable credentials.
 *
 * <p>In SpiceDB this is a wrong, expired, or rotated API token -- the most common error a new
 * integration produces. It is distinct from {@link PermissionDeniedException}, which means the
 * caller was identified but is not allowed, and from a bare {@link SpiceDBException}, which may be
 * an internal server fault: refresh credentials on this one, page someone on that one.
 */
public class UnauthenticatedException extends SpiceDBException {

  public UnauthenticatedException(String message) {
    super(message);
  }

  public UnauthenticatedException(String message, Throwable cause) {
    super(message, cause);
  }
}
