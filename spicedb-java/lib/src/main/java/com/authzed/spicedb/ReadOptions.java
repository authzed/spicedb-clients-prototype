package com.authzed.spicedb;

import java.time.Duration;
import java.util.*;

public final class ReadOptions {
  private final String cursor;
  private final TransportOptions transport;
  private final Duration timeout;
  private final int pageSize;
  private final long limit;

  private ReadOptions(Builder b) {
    this.cursor = b.cursor;
    this.transport = b.transport;
    if (transport == null)
      throw new com.authzed.spicedb.errors.InvalidArgumentException("transport must not be null");
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

  /** Per-call transport options and response-metadata capture; see {@link TransportOptions}. */
  public TransportOptions transport() {
    return transport;
  }

  /**
   * Resume after this after-result cursor. Repeat the original filter, consistency and page size.
   */
  public String cursor() {
    return cursor;
  }

  public static final class Builder {
    private Builder() {}

    private String cursor = null;

    /**
     * Resume after this after-result cursor. Repeat the original filter, consistency and page size.
     */
    public Builder cursor(String value) {
      this.cursor = value;
      return this;
    }

    private TransportOptions transport = TransportOptions.none();

    /** Per-call transport options and response-metadata capture; see {@link TransportOptions}. */
    public Builder transport(TransportOptions value) {
      this.transport = value;
      return this;
    }

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
