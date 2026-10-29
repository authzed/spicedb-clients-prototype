package com.authzed.spicedb;

import com.authzed.spicedb.errors.LocalValidationException;
import java.time.Duration;
import java.util.*;

public final class ReadOptions {
  private final String cursor;
  private final TransportOptions transport;
  private final RetryPolicy retryPolicy;
  private final Paging paging;
  private final Duration timeout;
  private final int pageSize;
  private final long limit;

  private ReadOptions(Builder b) {
    this.cursor = b.cursor;
    this.transport = b.transport;
    this.retryPolicy = b.retryPolicy;
    this.paging = b.paging;
    if (paging == null) throw new LocalValidationException("paging must not be null");
    if (transport == null) throw new LocalValidationException("transport must not be null");
    this.timeout = b.timeout;
    if (timeout != null && (timeout.isZero() || timeout.isNegative()))
      throw new LocalValidationException("timeout must be positive");
    this.pageSize = b.pageSize;
    if (pageSize <= 0) throw new LocalValidationException("pageSize must be positive");
    this.limit = b.limit;
    if (limit < 0) throw new LocalValidationException("limit must not be negative");
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

  /** Automatic paging (the default) or a single request; see {@link Paging}. */
  public Paging paging() {
    return paging;
  }

  /**
   * The retry policy overriding the client's for this call, or {@code null} for the client's. See
   * {@link Builder#retryPolicy}.
   */
  public RetryPolicy retryPolicy() {
    return retryPolicy;
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

    private Paging paging = Paging.AUTOMATIC;

    /** Automatic paging (the default) or exactly one request; see {@link Paging}. */
    public Builder paging(Paging value) {
      this.paging = value;
      return this;
    }

    private RetryPolicy retryPolicy = null;

    /**
     * Overrides the client's retry policy for this one call, including stream establishment
     * (reopening before the first item). A policy with {@code maxRetries(0)} exempts this call,
     * such as one supervised watch, while the client keeps retrying everything else.
     *
     * <p>Mutations are not retried by default. Only a write whose transaction holds only touches
     * and no preconditions, and a schema write, are idempotent on replay and may opt in; every
     * other mutation given a policy with retries fails with {@code InvalidArgumentException} before
     * any request.
     */
    public Builder retryPolicy(RetryPolicy value) {
      this.retryPolicy = value;
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
