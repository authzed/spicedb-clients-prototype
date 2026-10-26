package com.authzed.spicedb;

import java.time.Duration;
import java.util.*;

public final class CheckOptions {
  private final Map<String, Object> context;
  private final Duration timeout;
  private final CheckTracing tracing;

  private CheckOptions(Builder b) {
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

  public static final class Builder {
    private Builder() {}

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
