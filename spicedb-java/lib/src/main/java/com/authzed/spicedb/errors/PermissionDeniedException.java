package com.authzed.spicedb.errors;

/** The caller does not have permission to execute the operation. */
public class PermissionDeniedException extends SpiceDBException {

    public PermissionDeniedException(String message) {
        super(message);
    }

    public PermissionDeniedException(String message, Throwable cause) {
        super(message, cause);
    }
}
