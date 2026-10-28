package com.authzed.spicedb;

import java.time.Duration;

public final class PermissionSetOptions {
  private final TransportOptions transport;
  private final RetryPolicy retryPolicy;
  private final Duration timeout;
  private final int limit;
  private final String resumeCursor;

  private PermissionSetOptions(Builder b) {
    this.transport = b.transport;
    this.retryPolicy = b.retryPolicy;
    if (transport == null)
      throw new com.authzed.spicedb.errors.InvalidArgumentException("transport must not be null");
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

  public static final class Builder {
    private Duration timeout;
    private int limit = 512;
    private String resumeCursor;

    private Builder() {}

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
