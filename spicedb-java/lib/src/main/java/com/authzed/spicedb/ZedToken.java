package com.authzed.spicedb;

/** Opaque revision token. */
public record ZedToken(String value) {
  public ZedToken {
    if (value == null)
      throw new com.authzed.spicedb.errors.InvalidArgumentException("token must not be null");
  }

  @Override
  public String toString() {
    return value;
  }
}
