package com.authzed.spicedb;

import java.time.Duration;
import java.util.*;

public final class WriteOptions {
  private final TransportOptions transport;
  private final Duration timeout;
  private final Map<String, Object> metadata;

  private WriteOptions(Builder b) {
    this.transport = b.transport;
    if (transport == null)
      throw new com.authzed.spicedb.errors.InvalidArgumentException("transport must not be null");
    this.timeout = b.timeout;
    if (timeout != null && (timeout.isZero() || timeout.isNegative()))
      throw new com.authzed.spicedb.errors.InvalidArgumentException("timeout must be positive");
    this.metadata = JsonContext.snapshot(b.metadata);
  }

  public static Builder builder() {
    return new Builder();
  }

  public Duration timeout() {
    return timeout;
  }

  public Map<String, Object> metadata() {
    return metadata;
  }

  /** Per-call transport options and response-metadata capture; see {@link TransportOptions}. */
  public TransportOptions transport() {
    return transport;
  }

  public static final class Builder {
    private Builder() {}

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

    private Map<String, Object> metadata = null;

    public Builder metadata(Map<String, Object> value) {
      this.metadata = JsonContext.snapshot(value);
      return this;
    }

    public WriteOptions build() {
      return new WriteOptions(this);
    }
  }
}
