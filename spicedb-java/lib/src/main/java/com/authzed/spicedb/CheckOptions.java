package com.authzed.spicedb;

import java.time.Duration;
import java.util.*;

public final class CheckOptions {
  private final BulkItemErrors bulkItemErrors;
  private final TransportOptions transport;
  private final Map<String, Object> context;
  private final Duration timeout;
  private final CheckTracing tracing;

  private CheckOptions(Builder b) {
    this.bulkItemErrors = b.bulkItemErrors;
    if (bulkItemErrors == null)
      throw new com.authzed.spicedb.errors.InvalidArgumentException(
          "bulkItemErrors must not be null");
    this.transport = b.transport;
    if (transport == null)
      throw new com.authzed.spicedb.errors.InvalidArgumentException("transport must not be null");
    this.context = JsonContext.snapshot(b.context);
    this.timeout = b.timeout;
    if (timeout != null && (timeout.isZero() || timeout.isNegative()))
      throw new com.authzed.spicedb.errors.InvalidArgumentException("timeout must be positive");
    this.tracing = b.tracing;
    if (tracing == null)
      throw new com.authzed.spicedb.errors.InvalidArgumentException("tracing must not be null");
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
