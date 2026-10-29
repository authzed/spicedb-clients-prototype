package com.authzed.spicedb;

import com.authzed.spicedb.errors.LocalValidationException;
import java.time.Duration;
import java.util.*;

public final class DeleteAllOptions {
  private final java.util.function.Consumer<SpiceDBClient.DeleteResult> onProgress;
  private final TransportOptions transport;
  private final RetryPolicy retryPolicy;
  private final Duration timeout;
  private final String resumeCursor;
  private final int pageSize;
  private final List<Filter> mustMatch;
  private final List<Filter> mustNotMatch;

  private DeleteAllOptions(Builder b) {
    this.onProgress = b.onProgress;
    this.transport = b.transport;
    this.retryPolicy = b.retryPolicy;
    if (transport == null) throw new LocalValidationException("transport must not be null");
    this.timeout = b.timeout;
    if (timeout != null && (timeout.isZero() || timeout.isNegative()))
      throw new LocalValidationException("timeout must be positive");
    this.resumeCursor = b.resumeCursor;
    this.pageSize = b.pageSize;
    if (pageSize <= 0) throw new LocalValidationException("pageSize must be positive");
    this.mustMatch = List.copyOf(b.mustMatch);
    this.mustNotMatch = List.copyOf(b.mustNotMatch);
  }

  public static Builder builder() {
    return new Builder();
  }

  public Duration timeout() {
    return timeout;
  }

  public String resumeCursor() {
    return resumeCursor;
  }

  public int pageSize() {
    return pageSize;
  }

  public List<Filter> mustMatch() {
    return mustMatch;
  }

  public List<Filter> mustNotMatch() {
    return mustNotMatch;
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

  /** Called after each acknowledged batch with the cumulative count, latest revision and cursor. */
  public java.util.function.Consumer<SpiceDBClient.DeleteResult> onProgress() {
    return onProgress;
  }

  public static final class Builder {
    private Builder() {}

    private java.util.function.Consumer<SpiceDBClient.DeleteResult> onProgress = null;

    /**
     * Called after each acknowledged batch with the cumulative count, latest revision and cursor.
     */
    public Builder onProgress(java.util.function.Consumer<SpiceDBClient.DeleteResult> value) {
      this.onProgress = value;
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

    private String resumeCursor = null;

    public Builder resumeCursor(String value) {
      this.resumeCursor = value;
      return this;
    }

    private int pageSize = 1000;

    public Builder pageSize(int value) {
      this.pageSize = value;
      return this;
    }

    private List<Filter> mustMatch = List.of();

    public Builder mustMatch(List<Filter> value) {
      this.mustMatch = List.copyOf(value);
      return this;
    }

    private List<Filter> mustNotMatch = List.of();

    public Builder mustNotMatch(List<Filter> value) {
      this.mustNotMatch = List.copyOf(value);
      return this;
    }

    public DeleteAllOptions build() {
      return new DeleteAllOptions(this);
    }
  }
}
