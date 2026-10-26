package com.authzed.spicedb;

import java.time.Duration;
import java.util.*;

public final class CallOptions {
  private final Duration timeout;

  private CallOptions(Builder b) {
    this.timeout = b.timeout;
    if (timeout != null && (timeout.isZero() || timeout.isNegative()))
      throw new com.authzed.spicedb.errors.InvalidArgumentException("timeout must be positive");
  }

  public static Builder builder() {
    return new Builder();
  }

  public Duration timeout() {
    return timeout;
  }

  public static final class Builder {
    private Builder() {}

    private Duration timeout = null;

    public Builder timeout(Duration value) {
      this.timeout = value;
      return this;
    }

    public CallOptions build() {
      return new CallOptions(this);
    }
  }
}
