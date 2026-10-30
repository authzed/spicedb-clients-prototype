package com.authzed.spicedb;

import com.authzed.spicedb.errors.LocalValidationException;
import java.time.Duration;
import java.util.*;

public final class ImportOptions {
  private final TransportOptions transport;
  private final RetryPolicy retryPolicy;
  private final Duration timeout;
  private final int batchSize;

  private ImportOptions(Builder b) {
    this.transport = b.transport;
    this.retryPolicy = b.retryPolicy;
    if (transport == null) throw new LocalValidationException("transport must not be null");
    this.timeout = b.timeout;
    if (timeout != null && (timeout.isZero() || timeout.isNegative()))
      throw new LocalValidationException("timeout must be positive");
    this.batchSize = b.batchSize;
    if (batchSize <= 0) throw new LocalValidationException("batchSize must be positive");
  }

  public static Builder builder() {
    return new Builder();
  }

  public Duration timeout() {
    return timeout;
  }

  public int batchSize() {
    return batchSize;
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

    private Duration timeout = null;

    public Builder timeout(Duration value) {
      this.timeout = value;
      return this;
    }

    private int batchSize = 1000;

    public Builder batchSize(int value) {
      this.batchSize = value;
      return this;
    }

    public ImportOptions build() {
      return new ImportOptions(this);
    }
  }
}
