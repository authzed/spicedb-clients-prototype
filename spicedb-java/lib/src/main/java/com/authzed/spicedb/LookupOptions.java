package com.authzed.spicedb;

import com.authzed.spicedb.errors.LocalValidationException;
import java.time.Duration;
import java.util.*;

public final class LookupOptions {
  private final String cursor;
  private final TransportOptions transport;
  private final RetryPolicy retryPolicy;
  private final Paging paging;
  private final LookupPermissionshipFilter permissionshipFilter;
  private final Duration timeout;
  private final Map<String, Object> context;
  private final LookupDebug debug;
  private final int pageSize;
  private final long limit;
  private final String subjectRelation;
  private final Integer responseBatchSize;
  private final LookupBatching batching;

  private LookupOptions(Builder b) {
    this.cursor = b.cursor;
    this.transport = b.transport;
    this.retryPolicy = b.retryPolicy;
    this.paging = b.paging;
    if (paging == null) throw new LocalValidationException("paging must not be null");
    if (transport == null) throw new LocalValidationException("transport must not be null");
    this.permissionshipFilter = b.permissionshipFilter;
    if (permissionshipFilter == null)
      throw new LocalValidationException("permissionshipFilter must not be null");
    this.subjectRelation = b.subjectRelation;
    if (subjectRelation != null) {
      JsonContext.required(subjectRelation, "subjectRelation");
      if (subjectRelation.equals("..."))
        throw new LocalValidationException("direct subject relation is null");
    }
    this.timeout = b.timeout;
    if (timeout != null && (timeout.isZero() || timeout.isNegative()))
      throw new LocalValidationException("timeout must be positive");
    this.context = JsonContext.snapshot(b.context);
    this.debug = b.debug;
    if (debug == null) throw new LocalValidationException("debug must not be null");
    this.pageSize = b.pageSize;
    if (pageSize <= 0) throw new LocalValidationException("pageSize must be positive");
    this.limit = b.limit;
    if (limit < 0) throw new LocalValidationException("limit must not be negative");
    this.batching = b.batching;
    if (batching == null) throw new LocalValidationException("batching must not be null");
    this.responseBatchSize = b.responseBatchSize;
    if (responseBatchSize != null && responseBatchSize <= 0)
      throw new LocalValidationException("responseBatchSize must be positive");
  }

  public String subjectRelation() {
    return subjectRelation;
  }

  public Builder toBuilder() {
    var b = builder().batching(batching);
    b.responseBatchSize = responseBatchSize;
    return b.permissionshipFilter(permissionshipFilter)
        .timeout(timeout)
        .context(context)
        .debug(debug)
        .pageSize(pageSize)
        .limit(limit)
        .subjectRelation(subjectRelation)
        .cursor(cursor)
        .paging(paging)
        .retryPolicy(retryPolicy)
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

  /** Batched lookupResources responses (the default) or disabled; see {@link LookupBatching}. */
  public LookupBatching batching() {
    return batching;
  }

  /**
   * The response batch size a lookupResources call with these options requests, or {@code null}
   * when batching is {@link LookupBatching#DISABLED}: the size set with {@link
   * Builder#responseBatchSize}, otherwise the page size capped at 1,000 (with {@link
   * Paging#SINGLE_REQUEST}: the limit capped at 1,000, or 1,000 without one). A limited call may
   * request a smaller size, or none, for a page; see {@link #responseBatchSizeForPage}.
   */
  public Integer responseBatchSize() {
    if (batching == LookupBatching.DISABLED) return null;
    if (responseBatchSize != null) return responseBatchSize;
    if (paging != Paging.SINGLE_REQUEST) return Math.min(pageSize, MAX_DEFAULT_RESPONSE_BATCH_SIZE);
    return limit > 0
        ? (int) Math.min(limit, MAX_DEFAULT_RESPONSE_BATCH_SIZE)
        : MAX_DEFAULT_RESPONSE_BATCH_SIZE;
  }

  // The server's default maximum response batch size; it rejects a larger one.
  private static final int MAX_DEFAULT_RESPONSE_BATCH_SIZE = 1000;

  /**
   * The response batch size sent with a lookupResources page request once {@code yielded} results
   * have been yielded; 0 requests no batching for that page.
   *
   * <p>Only the last result of a batch is a resume point, and a total limit must end on one. So
   * when automatic paging has fewer results left to yield than a page holds, the page's batch size
   * is the remaining count if that is smaller than the batch size, unchanged if it divides the
   * remaining count, and otherwise the page is not batched. With {@link
   * LookupPermissionshipFilter#HAS_PERMISSION_ONLY} the filter decides which results count toward
   * the limit, so a limited call is not batched at all.
   */
  public int responseBatchSizeForPage(long yielded) {
    var configured = responseBatchSize();
    int size = configured == null ? 0 : configured;
    if (size == 0 || limit == 0 || paging != Paging.AUTOMATIC) return size;
    if (permissionshipFilter == LookupPermissionshipFilter.HAS_PERMISSION_ONLY) return 0;
    long remaining = limit - yielded;
    if (remaining >= pageSize || remaining % size == 0) return size;
    return remaining < size ? (int) Math.max(remaining, 0) : 0;
  }

  /** Automatic paging (the default) or a single request; see {@link Paging}. */
  public Paging paging() {
    return paging;
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

  /**
   * Resume lookupResources after this after-result cursor. Repeat the original request and page
   * size; the response batch size may differ.
   */
  public String cursor() {
    return cursor;
  }

  public static final class Builder {
    private Builder() {}

    private String cursor = null;

    /**
     * Resume lookupResources after this after-result cursor. Repeat the original request and page
     * size; the response batch size may differ.
     */
    public Builder cursor(String value) {
      this.cursor = value;
      return this;
    }

    private Paging paging = Paging.AUTOMATIC;

    /** Automatic paging (the default) or exactly one request; see {@link Paging}. */
    public Builder paging(Paging value) {
      this.paging = value;
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

    private Integer responseBatchSize = null;

    /**
     * How many resources lookupResources asks the server to pack into each response message; must
     * be positive. Sent as given: the server rejects a size above its maximum (1,000 by default).
     * Unset, the size is the page size capped at 1,000; see {@link
     * LookupOptions#responseBatchSize}.
     *
     * <p>Results are still yielded one at a time, but only the last one yielded from each batch
     * carries an {@code afterResultCursor}. Ignored with {@link LookupBatching#DISABLED}.
     * lookupResources only.
     */
    public Builder responseBatchSize(int value) {
      this.responseBatchSize = value;
      return this;
    }

    private LookupBatching batching = LookupBatching.AUTOMATIC;

    /**
     * Batched lookupResources responses (the default) or {@link LookupBatching#DISABLED}, which
     * makes every item carry its after-result cursor. lookupResources only.
     */
    public Builder batching(LookupBatching value) {
      this.batching = value;
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
