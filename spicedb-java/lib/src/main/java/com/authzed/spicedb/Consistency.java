package com.authzed.spicedb;

import com.authzed.spicedb.errors.LocalValidationException;

/**
 * Consistency strategies for SpiceDB read operations.
 *
 * <p>Every read operation requires an explicit consistency strategy. This prevents silent defaults
 * and makes consistency choices visible in the calling code.
 *
 * <p>ZedTokens are represented as opaque native {@code ZedToken} values, never proto types.
 */
public final class Consistency {

  private final com.authzed.api.v1.Consistency proto;

  private Consistency(com.authzed.api.v1.Consistency proto) {
    this.proto = proto;
  }

  /**
   * Returns the underlying proto Consistency for use by the client. Package-private: proto types
   * must never appear in the public API surface.
   */
  com.authzed.api.v1.Consistency toProto() {
    return proto;
  }

  /** The strategy a {@link Consistency} names. */
  public enum Kind {
    FULL,
    MIN_LATENCY,
    AT_LEAST,
    SNAPSHOT
  }

  /** Which strategy this is. */
  public Kind kind() {
    return switch (proto.getRequirementCase()) {
      case MINIMIZE_LATENCY -> Kind.MIN_LATENCY;
      case AT_LEAST_AS_FRESH -> Kind.AT_LEAST;
      case AT_EXACT_SNAPSHOT -> Kind.SNAPSHOT;
      default -> Kind.FULL;
    };
  }

  /** The revision for {@code AT_LEAST} and {@code SNAPSHOT}; {@code null} otherwise. */
  public ZedToken token() {
    return switch (kind()) {
      case AT_LEAST -> new ZedToken(proto.getAtLeastAsFresh().getToken());
      case SNAPSHOT -> new ZedToken(proto.getAtExactSnapshot().getToken());
      default -> null;
    };
  }

  /** E.g. {@code full}, {@code minLatency}, {@code atLeast(<token>)}, {@code snapshot(<token>)}. */
  @Override
  public String toString() {
    return switch (kind()) {
      case FULL -> "full";
      case MIN_LATENCY -> "minLatency";
      case AT_LEAST -> "atLeast(" + token() + ")";
      case SNAPSHOT -> "snapshot(" + token() + ")";
    };
  }

  /**
   * Structured JSON diagnostic form, for structured loggers: {@code
   * {"strategy":"atLeast","token":"<token>"}}, with no token for {@code full} and {@code
   * minLatency}.
   */
  public String toJson() {
    var m = new java.util.LinkedHashMap<String, Object>();
    m.put(
        "strategy",
        switch (kind()) {
          case FULL -> "full";
          case MIN_LATENCY -> "minLatency";
          case AT_LEAST -> "atLeast";
          case SNAPSHOT -> "snapshot";
        });
    ZedToken token = token();
    if (token != null) m.put("token", token.toString());
    return Json.write(m);
  }

  /**
   * Returns a strategy that requires full consistency. This is the least performant option but
   * guarantees the most up-to-date results.
   */
  public static Consistency full() {
    return new Consistency(
        com.authzed.api.v1.Consistency.newBuilder().setFullyConsistent(true).build());
  }

  /**
   * Returns a strategy that uses SpiceDB's preferred revision for optimal performance. This is the
   * recommended default for most read operations.
   */
  public static Consistency minLatency() {
    return new Consistency(
        com.authzed.api.v1.Consistency.newBuilder().setMinimizeLatency(true).build());
  }

  /**
   * Returns a strategy that ensures results are at least as fresh as the given revision. Use this
   * for read-after-write consistency.
   *
   * @param revision an opaque ZedToken value from a previous write
   * @throws com.authzed.spicedb.errors.InvalidArgumentException if revision is null, empty or
   *     whitespace-only
   */
  public static Consistency atLeast(ZedToken revision) {
    if (revision == null || revision.value().isBlank()) {
      throw new LocalValidationException("revision must not be null, empty or whitespace-only");
    }
    return new Consistency(
        com.authzed.api.v1.Consistency.newBuilder()
            .setAtLeastAsFresh(
                com.authzed.api.v1.ZedToken.newBuilder().setToken(revision.value()).build())
            .build());
  }

  /**
   * Returns a strategy that reads at the exact given revision.
   *
   * @param revision an opaque ZedToken value
   * @throws com.authzed.spicedb.errors.InvalidArgumentException if revision is null, empty or
   *     whitespace-only
   */
  public static Consistency snapshot(ZedToken revision) {
    if (revision == null || revision.value().isBlank()) {
      throw new LocalValidationException("revision must not be null, empty or whitespace-only");
    }
    return new Consistency(
        com.authzed.api.v1.Consistency.newBuilder()
            .setAtExactSnapshot(
                com.authzed.api.v1.ZedToken.newBuilder().setToken(revision.value()).build())
            .build());
  }

  /**
   * Returns {@link #atLeast(ZedToken)} if revision is non-null and not blank, otherwise {@link
   * #full()}. Use this when you have an optional revision from a previous write and want the safest
   * fallback.
   *
   * @param revision an opaque ZedToken value, or null, empty or whitespace-only
   */
  public static Consistency atLeastOrFull(ZedToken revision) {
    if (revision == null || revision.value().isBlank()) {
      return full();
    }
    return atLeast(revision);
  }

  /**
   * Returns {@link #atLeast(ZedToken)} if revision is non-null and not blank, otherwise {@link
   * #minLatency()}. Use this when you have an optional revision but prefer performance over full
   * consistency as the fallback.
   *
   * @param revision an opaque ZedToken value, or null, empty or whitespace-only
   */
  public static Consistency atLeastOrMinLatency(ZedToken revision) {
    if (revision == null || revision.value().isBlank()) {
      return minLatency();
    }
    return atLeast(revision);
  }

  @Override
  public boolean equals(Object other) {
    return other instanceof Consistency c && proto.equals(c.proto);
  }

  @Override
  public int hashCode() {
    return proto.hashCode();
  }

  private Consistency() {
    throw new UnsupportedOperationException();
  }
}
