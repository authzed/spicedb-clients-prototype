package com.authzed.spicedb;

import com.authzed.spicedb.errors.LocalValidationException;
import java.time.Duration;
import java.util.*;

public final class CheckOptions {
  private final BulkItemErrors bulkItemErrors;
  private final TransportOptions transport;
  private final RetryPolicy retryPolicy;
  private final Map<String, Object> context;
  private final Duration timeout;
  private final CheckTracing tracing;

  private CheckOptions(Builder b) {
    this.bulkItemErrors = b.bulkItemErrors;
    if (bulkItemErrors == null)
      throw new LocalValidationException("bulkItemErrors must not be null");
    this.transport = b.transport;
    this.retryPolicy = b.retryPolicy;
    if (transport == null) throw new LocalValidationException("transport must not be null");
    this.context = JsonContext.snapshot(b.context);
    this.timeout = b.timeout;
    if (timeout != null && (timeout.isZero() || timeout.isNegative()))
      throw new LocalValidationException("timeout must be positive");
    this.tracing = b.tracing;
    if (tracing == null) throw new LocalValidationException("tracing must not be null");
  }

  public static Builder builder() {
    return new Builder();
  }

  public Map<String, Object> context() {
    return context;
  }

  public Duration timeout() {
    return timeout;
  }

  public CheckTracing tracing() {
    return tracing;
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

  /** How bulk checks report per-item errors (default {@link BulkItemErrors#FAIL_CALL}). */
  public BulkItemErrors bulkItemErrors() {
    return bulkItemErrors;
  }

  public static final class Builder {
    private Builder() {}

    private BulkItemErrors bulkItemErrors = BulkItemErrors.FAIL_CALL;

    /** How bulk checks report per-item errors (default {@link BulkItemErrors#FAIL_CALL}). */
    public Builder bulkItemErrors(BulkItemErrors value) {
      this.bulkItemErrors = value;
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

    private Map<String, Object> context = null;

    public Builder context(Map<String, Object> value) {
      this.context = JsonContext.snapshot(value);
      return this;
    }

    private Duration timeout = null;

    public Builder timeout(Duration value) {
      this.timeout = value;
      return this;
    }

    private CheckTracing tracing = CheckTracing.DISABLED;

    public Builder tracing(CheckTracing value) {
      this.tracing = value;
      return this;
    }

    public CheckOptions build() {
      return new CheckOptions(this);
    }
  }
}
