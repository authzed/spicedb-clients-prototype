package com.authzed.spicedb;

import java.time.Duration;
import java.util.*;

public final class LookupOptions {
  private final String cursor;
  private final TransportOptions transport;
  private final LookupPermissionshipFilter permissionshipFilter;
  private final Duration timeout;
  private final Map<String, Object> context;
  private final LookupDebug debug;
  private final int pageSize;
  private final long limit;
  private final String subjectRelation;

  private LookupOptions(Builder b) {
    this.cursor = b.cursor;
    this.transport = b.transport;
    if (transport == null)
      throw new com.authzed.spicedb.errors.InvalidArgumentException("transport must not be null");
    this.permissionshipFilter = b.permissionshipFilter;
    if (permissionshipFilter == null)
      throw new com.authzed.spicedb.errors.InvalidArgumentException(
          "permissionshipFilter must not be null");
    this.subjectRelation = b.subjectRelation;
    if (subjectRelation != null) {
      JsonContext.required(subjectRelation, "subjectRelation");
      if (subjectRelation.equals("..."))
        throw new com.authzed.spicedb.errors.InvalidArgumentException(
            "direct subject relation is null");
    }
    this.timeout = b.timeout;
    if (timeout != null && (timeout.isZero() || timeout.isNegative()))
      throw new com.authzed.spicedb.errors.InvalidArgumentException("timeout must be positive");
    this.context = JsonContext.snapshot(b.context);
    this.debug = b.debug;
    if (debug == null)
      throw new com.authzed.spicedb.errors.InvalidArgumentException("debug must not be null");
    this.pageSize = b.pageSize;
    if (pageSize <= 0)
      throw new com.authzed.spicedb.errors.InvalidArgumentException("pageSize must be positive");
    this.limit = b.limit;
    if (limit < 0)
      throw new com.authzed.spicedb.errors.InvalidArgumentException("limit must not be negative");
  }

  public String subjectRelation() {
    return subjectRelation;
  }

  public Builder toBuilder() {
    return builder()
        .permissionshipFilter(permissionshipFilter)
        .timeout(timeout)
        .context(context)
        .debug(debug)
        .pageSize(pageSize)
        .limit(limit)
        .subjectRelation(subjectRelation)
        .cursor(cursor)
        .transport(transport);
  }

  public static Builder builder() {
    return new Builder();
  }

  public LookupPermissionshipFilter permissionshipFilter() {
    return permissionshipFilter;
  }

  public Duration timeout() {
    return timeout;
  }

  public Map<String, Object> context() {
    return context;
  }

  public LookupDebug debug() {
    return debug;
  }

  public int pageSize() {
    return pageSize;
  }

  public long limit() {
    return limit;
  }

  /** Per-call transport options and response-metadata capture; see {@link TransportOptions}. */
  public TransportOptions transport() {
    return transport;
  }

  /**
   * Resume lookupResources after this after-result cursor. Repeat the original request and page
   * size.
   */
  public String cursor() {
    return cursor;
  }

  public static final class Builder {
    private Builder() {}

    private String cursor = null;

    /**
     * Resume lookupResources after this after-result cursor. Repeat the original request and page
     * size.
     */
    public Builder cursor(String value) {
      this.cursor = value;
      return this;
    }

    private TransportOptions transport = TransportOptions.none();

    /** Per-call transport options and response-metadata capture; see {@link TransportOptions}. */
    public Builder transport(TransportOptions value) {
      this.transport = value;
      return this;
    }

    private String subjectRelation;

    public Builder subjectRelation(String value) {
      this.subjectRelation = value;
      return this;
    }

    private LookupPermissionshipFilter permissionshipFilter = LookupPermissionshipFilter.ALL;

    public Builder permissionshipFilter(LookupPermissionshipFilter value) {
      this.permissionshipFilter = value;
      return this;
    }

    private Duration timeout = null;

    public Builder timeout(Duration value) {
      this.timeout = value;
      return this;
    }

    private Map<String, Object> context = null;

    public Builder context(Map<String, Object> value) {
      this.context = JsonContext.snapshot(value);
      return this;
    }

    private LookupDebug debug = LookupDebug.DISABLED;

    public Builder debug(LookupDebug value) {
      this.debug = value;
      return this;
    }

    private int pageSize = 512;

    public Builder pageSize(int value) {
      this.pageSize = value;
      return this;
    }

    private long limit = 0;

    public Builder limit(long value) {
      this.limit = value;
      return this;
    }

    public LookupOptions build() {
      return new LookupOptions(this);
    }
  }
}
