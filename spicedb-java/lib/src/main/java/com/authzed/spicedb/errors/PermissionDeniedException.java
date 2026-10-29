package com.authzed.spicedb.errors;

/** The caller does not have permission to execute the operation. */
public class PermissionDeniedException extends SpiceDBException {

  public PermissionDeniedException(String message) {
    super(io.grpc.Status.Code.PERMISSION_DENIED, message, null);
  }

  public PermissionDeniedException(String message, Throwable cause) {
    super(io.grpc.Status.Code.PERMISSION_DENIED, message, cause);
  }
}
