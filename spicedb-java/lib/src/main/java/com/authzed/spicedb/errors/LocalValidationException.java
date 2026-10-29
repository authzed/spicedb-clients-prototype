package com.authzed.spicedb.errors;

/**
 * Input this client rejected itself, before any request: an invalid argument, option, tuple or
 * endpoint. Its code is {@code INVALID_ARGUMENT}, like the server's own rejection, which is a plain
 * {@link InvalidArgumentException}; catch this subclass to handle local validation only.
 *
 * <p>Java's single class inheritance keeps it from also being an {@link IllegalArgumentException}:
 * code that caught {@code IllegalArgumentException} around parsing catches this (or {@link
 * InvalidArgumentException}) instead.
 */
public class LocalValidationException extends InvalidArgumentException {

  public LocalValidationException(String message) {
    super(message);
  }

  public LocalValidationException(String message, Throwable cause) {
    super(message, cause);
  }
}
