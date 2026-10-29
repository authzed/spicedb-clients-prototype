package com.authzed.spicedb;

import com.authzed.spicedb.errors.LocalValidationException;

/** Opaque revision token. */
public record ZedToken(String value) {
  public ZedToken {
    if (value == null) throw new LocalValidationException("token must not be null");
  }

  @Override
  public String toString() {
    return value;
  }
}
