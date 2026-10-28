package com.authzed.spicedb;

import com.authzed.spicedb.errors.InvalidArgumentException;
import io.grpc.Status;
import java.time.Duration;
import java.util.EnumSet;
import java.util.Set;
import java.util.function.Consumer;

/**
 * Client-wide retry policy for idempotent reads (and, unless {@link StreamRetries#DISABLED}, stream
 * establishment). Mutations -- writes, deletes, imports, counter registration -- are never replayed
 * under any policy.
 *
 * <p>The default retries {@code UNAVAILABLE} and {@code ABORTED} up to three times with full-jitter
 * exponential backoff starting at 100 ms. A policy may opt into other read codes, such as {@code
 * RESOURCE_EXHAUSTED}. Every retry shares the operation's deadline and cancellation. Use {@link
 * Retries#DISABLED} to turn retries off entirely.
 *
 * <pre>{@code
 * var policy = RetryPolicy.builder()
 *     .maxRetries(2)
 *     .retryableCodes(Set.of(Status.Code.UNAVAILABLE, Status.Code.RESOURCE_EXHAUSTED))
 *     .onRetry(attempt -> log.warn("retrying {}", attempt))
 *     .build();
 * ClientOptions.builder().retryPolicy(policy).build();
 * }</pre>
 */
public final class RetryPolicy {

  /** One scheduled retry, reported to {@link Builder#onRetry} before the backoff sleep. */
  public record RetryAttempt(int retry, Status.Code code, Duration backoff, Throwable error) {}

  private static final RetryPolicy DEFAULT = builder().build();

  private final int maxRetries;
  private final Set<Status.Code> retryableCodes;
  private final Duration initialBackoff;
  private final Duration maxBackoff;
  private final Consumer<RetryAttempt> onRetry;

  private RetryPolicy(Builder b) {
    if (b.maxRetries < 0) throw new InvalidArgumentException("maxRetries must not be negative");
    if (b.retryableCodes == null)
      throw new InvalidArgumentException("retryableCodes must not be null");
    if (b.initialBackoff == null || b.initialBackoff.isNegative() || b.initialBackoff.isZero())
      throw new InvalidArgumentException("initialBackoff must be positive");
    if (b.maxBackoff == null || b.maxBackoff.compareTo(b.initialBackoff) < 0)
      throw new InvalidArgumentException("maxBackoff must be at least initialBackoff");
    maxRetries = b.maxRetries;
    retryableCodes =
        b.retryableCodes.isEmpty()
            ? Set.of()
            : java.util.Collections.unmodifiableSet(EnumSet.copyOf(b.retryableCodes));
    initialBackoff = b.initialBackoff;
    maxBackoff = b.maxBackoff;
    onRetry = b.onRetry;
  }

  /** The default policy: three retries of {@code UNAVAILABLE}/{@code ABORTED}, 100 ms backoff. */
  public static RetryPolicy defaults() {
    return DEFAULT;
  }

  public static Builder builder() {
    return new Builder();
  }

  public int maxRetries() {
    return maxRetries;
  }

  public Set<Status.Code> retryableCodes() {
    return retryableCodes;
  }

  public Duration initialBackoff() {
    return initialBackoff;
  }

  public Duration maxBackoff() {
    return maxBackoff;
  }

  /** The retry callback, or {@code null}. */
  public Consumer<RetryAttempt> onRetry() {
    return onRetry;
  }

  public static final class Builder {
    private int maxRetries = 3;
    private Set<Status.Code> retryableCodes = Set.of(Status.Code.UNAVAILABLE, Status.Code.ABORTED);
    private Duration initialBackoff = Duration.ofMillis(100);
    private Duration maxBackoff = Duration.ofSeconds(5);
    private Consumer<RetryAttempt> onRetry;

    private Builder() {}

    /** Retries after the first attempt; {@code 0} makes one attempt. */
    public Builder maxRetries(int value) {
      maxRetries = value;
      return this;
    }

    /** Status codes that make a read retryable. Snapshotted. */
    public Builder retryableCodes(Set<Status.Code> value) {
      if (value != null && value.stream().anyMatch(java.util.Objects::isNull))
        throw new InvalidArgumentException("retryableCodes must not contain null");
      retryableCodes = value == null ? null : Set.copyOf(value);
      return this;
    }

    /** Cap of the first backoff; each later cap doubles, up to {@link #maxBackoff}. */
    public Builder initialBackoff(Duration value) {
      initialBackoff = value;
      return this;
    }

    public Builder maxBackoff(Duration value) {
      maxBackoff = value;
      return this;
    }

    /** Called on the calling thread before each retry's backoff, e.g. for logging. */
    public Builder onRetry(Consumer<RetryAttempt> value) {
      onRetry = value;
      return this;
    }

    public RetryPolicy build() {
      return new RetryPolicy(this);
    }
  }
}
