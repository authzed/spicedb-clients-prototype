package com.authzed.spicedb;

import com.authzed.spicedb.errors.LocalValidationException;
import java.time.Duration;
import java.util.*;

public final class WatchOptions {
  private final WatchSchemaChanges schemaChanges;
  private final TransportOptions transport;
  private final RetryPolicy retryPolicy;
  private final List<Filter> relationshipFilters;
  private final Duration timeout;
  private final WatchContent content;

  private WatchOptions(Builder b) {
    this.schemaChanges = b.schemaChanges;
    if (schemaChanges == null) throw new LocalValidationException("schemaChanges must not be null");
    this.transport = b.transport;
    this.retryPolicy = b.retryPolicy;
    if (transport == null) throw new LocalValidationException("transport must not be null");
    this.relationshipFilters = b.relationshipFilters;
    this.timeout = b.timeout;
    if (timeout != null && (timeout.isZero() || timeout.isNegative()))
      throw new LocalValidationException("timeout must be positive");
    this.content = b.content;
    if (content == null) throw new LocalValidationException("content must not be null");
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

  /**
   * The retry policy for this watch, or {@code null} for the {@link ClientOperation#WATCH}
   * operation policy, else never reopening. See {@link Builder#retryPolicy}.
   */
  public RetryPolicy retryPolicy() {
    return retryPolicy;
  }

  /** Per-call transport options and response-metadata capture; see {@link TransportOptions}. */
  public TransportOptions transport() {
    return transport;
  }

  /** Whether to request schema-change events (default {@link WatchSchemaChanges#EXCLUDED}). */
  public WatchSchemaChanges schemaChanges() {
    return schemaChanges;
  }

  public static final class Builder {
    private Builder() {}

    private WatchSchemaChanges schemaChanges = WatchSchemaChanges.EXCLUDED;

    /** Whether to request schema-change events (default {@link WatchSchemaChanges#EXCLUDED}). */
    public Builder schemaChanges(WatchSchemaChanges value) {
      this.schemaChanges = value;
      return this;
    }

    private RetryPolicy retryPolicy = null;

    /**
     * Sets the retry policy for this one watch, opting in to reopening it before its first event.
     * Without this or a {@link ClientOperation#WATCH} operation policy the watch is never reopened:
     * the client-wide policy does not apply, since a reopened watch without a start revision would
     * begin at a later revision and skip the changes between.
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

    private List<Filter> relationshipFilters = List.of();

    public Builder relationshipFilters(List<Filter> value) {
      if (value == null || value.stream().anyMatch(Objects::isNull))
        throw new LocalValidationException("relationshipFilters must contain nonnull filters");
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
