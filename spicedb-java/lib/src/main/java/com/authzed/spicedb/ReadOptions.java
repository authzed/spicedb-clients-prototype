package com.authzed.spicedb;

import java.time.Duration;
import java.util.*;

public final class ReadOptions {
  private final Duration timeout;
  private final int pageSize;
  private final long limit;

  private ReadOptions(Builder b) {
    this.timeout = b.timeout;
    if (timeout != null && (timeout.isZero() || timeout.isNegative()))
      throw new com.authzed.spicedb.errors.InvalidArgumentException("timeout must be positive");
    this.pageSize = b.pageSize;
    if (pageSize <= 0)
      throw new com.authzed.spicedb.errors.InvalidArgumentException("pageSize must be positive");
    this.limit = b.limit;
    if (limit < 0)
      throw new com.authzed.spicedb.errors.InvalidArgumentException("limit must not be negative");
  }

  public static Builder builder() {
    return new Builder();
  }

  public Duration timeout() {
    return timeout;
  }

  public int pageSize() {
    return pageSize;
  }

  public long limit() {
    return limit;
  }

  public static final class Builder {
    private Builder() {}

    private Duration timeout = null;

    public Builder timeout(Duration value) {
      this.timeout = value;
      return this;
    }

    private int pageSize = 512;

    public Builder pageSize(int value) {
      this.pageSize = value;
      return this;
    }

    private long limit = 0;

    public Builder limit(long value) {
      this.limit = value;
      return this;
    }

    public ReadOptions build() {
      return new ReadOptions(this);
    }
  }
}
