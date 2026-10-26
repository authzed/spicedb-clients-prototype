package com.authzed.spicedb;

import java.time.Duration;
import java.util.*;

public final class ImportOptions {
  private final Duration timeout;
  private final int batchSize;

  private ImportOptions(Builder b) {
    this.timeout = b.timeout;
    if (timeout != null && (timeout.isZero() || timeout.isNegative()))
      throw new com.authzed.spicedb.errors.InvalidArgumentException("timeout must be positive");
    this.batchSize = b.batchSize;
    if (batchSize <= 0)
      throw new com.authzed.spicedb.errors.InvalidArgumentException("batchSize must be positive");
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

  public static final class Builder {
    private Builder() {}

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
