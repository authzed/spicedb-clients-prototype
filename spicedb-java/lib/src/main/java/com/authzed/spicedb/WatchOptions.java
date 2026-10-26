package com.authzed.spicedb;

import java.time.Duration;
import java.util.*;

public final class WatchOptions {
  private final List<Filter> relationshipFilters;
  private final Duration timeout;
  private final WatchContent content;

  private WatchOptions(Builder b) {
    this.relationshipFilters = b.relationshipFilters;
    this.timeout = b.timeout;
    if (timeout != null && (timeout.isZero() || timeout.isNegative()))
      throw new com.authzed.spicedb.errors.InvalidArgumentException("timeout must be positive");
    this.content = b.content;
    if (content == null)
      throw new com.authzed.spicedb.errors.InvalidArgumentException("content must not be null");
  }

  public static Builder builder() {
    return new Builder();
  }

  public List<Filter> relationshipFilters() {
    return relationshipFilters;
  }

  public Duration timeout() {
    return timeout;
  }

  public WatchContent content() {
    return content;
  }

  public static final class Builder {
    private Builder() {}

    private List<Filter> relationshipFilters = List.of();

    public Builder relationshipFilters(List<Filter> value) {
      if (value == null || value.stream().anyMatch(Objects::isNull))
        throw new com.authzed.spicedb.errors.InvalidArgumentException(
            "relationshipFilters must contain nonnull filters");
      this.relationshipFilters = List.copyOf(value);
      return this;
    }

    private Duration timeout = null;

    public Builder timeout(Duration value) {
      this.timeout = value;
      return this;
    }

    private WatchContent content = WatchContent.RELATIONSHIPS;

    public Builder content(WatchContent value) {
      this.content = value;
      return this;
    }

    public WatchOptions build() {
      return new WatchOptions(this);
    }
  }
}
