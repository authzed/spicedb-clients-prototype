package com.authzed.spicedb;

import java.time.Duration;

public final class PermissionSetOptions {
  private final Duration timeout;
  private final int limit;
  private final String resumeCursor;

  private PermissionSetOptions(Builder b) {
    timeout = b.timeout;
    limit = b.limit;
    resumeCursor = b.resumeCursor;
    if (timeout != null && (timeout.isZero() || timeout.isNegative()))
      throw new com.authzed.spicedb.errors.InvalidArgumentException("timeout must be positive");
    if (limit <= 0)
      throw new com.authzed.spicedb.errors.InvalidArgumentException("limit must be positive");
  }

  public Duration timeout() {
    return timeout;
  }

  public int limit() {
    return limit;
  }

  public String resumeCursor() {
    return resumeCursor;
  }

  public static Builder builder() {
    return new Builder();
  }

  public static final class Builder {
    private Duration timeout;
    private int limit = 512;
    private String resumeCursor;

    private Builder() {}

    public Builder timeout(Duration value) {
      timeout = value;
      return this;
    }

    public Builder limit(int value) {
      limit = value;
      return this;
    }

    public Builder resumeCursor(String value) {
      resumeCursor = value;
      return this;
    }

    public PermissionSetOptions build() {
      return new PermissionSetOptions(this);
    }
  }
}
