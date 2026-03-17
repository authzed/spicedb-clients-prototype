package com.authzed.spicedb.errors;

/** The request contained an invalid argument. */
public class InvalidArgumentException extends SpiceDBException {

    public InvalidArgumentException(String message) {
        super(message);
    }

    public InvalidArgumentException(String message, Throwable cause) {
        super(message, cause);
    }
}
