package com.authzed.spicedb;

import java.time.Duration;
import java.util.*;

public final class WriteOptions {
  private final Duration timeout;
  private final Map<String, Object> metadata;

  private WriteOptions(Builder b) {
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

  public static final class Builder {
    private Builder() {}

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
