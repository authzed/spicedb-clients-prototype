package com.authzed.spicedb;

/**
 * Consistency strategies for SpiceDB read operations.
 *
 * <p>Every read operation requires an explicit consistency strategy. This prevents silent defaults
 * and makes consistency choices visible in the calling code.
 *
 * <p>ZedTokens are represented as opaque native {@code ZedToken} values, never proto types.
 */
public final class Consistency {

  private final build.buf.gen.authzed.api.v1.Consistency proto;

  private Consistency(build.buf.gen.authzed.api.v1.Consistency proto) {
    this.proto = proto;
  }

  /**
   * Returns the underlying proto Consistency for use by the client. Package-private: proto types
   * must never appear in the public API surface.
   */
  build.buf.gen.authzed.api.v1.Consistency toProto() {
    return proto;
  }

  /**
   * Returns a strategy that requires full consistency. This is the least performant option but
   * guarantees the most up-to-date results.
   */
  public static Consistency full() {
    return new Consistency(
        build.buf.gen.authzed.api.v1.Consistency.newBuilder().setFullyConsistent(true).build());
  }

  /**
   * Returns a strategy that uses SpiceDB's preferred revision for optimal performance. This is the
   * recommended default for most read operations.
   */
  public static Consistency minLatency() {
    return new Consistency(
        build.buf.gen.authzed.api.v1.Consistency.newBuilder().setMinimizeLatency(true).build());
  }

  /**
   * Returns a strategy that ensures results are at least as fresh as the given revision. Use this
   * for read-after-write consistency.
   *
   * @param revision an opaque ZedToken value from a previous write
   * @throws com.authzed.spicedb.errors.InvalidArgumentException if revision is null or empty
   */
  public static Consistency atLeast(ZedToken revision) {
    if (revision == null || revision.value().isEmpty()) {
      throw new com.authzed.spicedb.errors.InvalidArgumentException(
          "revision must not be null or empty");
    }
    return new Consistency(
        build.buf.gen.authzed.api.v1.Consistency.newBuilder()
            .setAtLeastAsFresh(
                build.buf.gen.authzed.api.v1.ZedToken.newBuilder()
                    .setToken(revision.value())
                    .build())
            .build());
  }

  /**
   * Returns a strategy that reads at the exact given revision.
   *
   * @param revision an opaque ZedToken value
   * @throws com.authzed.spicedb.errors.InvalidArgumentException if revision is null or empty
   */
  public static Consistency snapshot(ZedToken revision) {
    if (revision == null || revision.value().isEmpty()) {
      throw new com.authzed.spicedb.errors.InvalidArgumentException(
          "revision must not be null or empty");
    }
    return new Consistency(
        build.buf.gen.authzed.api.v1.Consistency.newBuilder()
            .setAtExactSnapshot(
                build.buf.gen.authzed.api.v1.ZedToken.newBuilder()
                    .setToken(revision.value())
                    .build())
            .build());
  }

  /**
   * Returns {@link #atLeast(ZedToken)} if revision is non-null and non-empty, otherwise {@link
   * #full()}. Use this when you have an optional revision from a previous write and want the safest
   * fallback.
   *
   * @param revision an opaque ZedToken value, or null/empty
   */
  public static Consistency atLeastOrFull(ZedToken revision) {
    if (revision == null || revision.value().isEmpty()) {
      return full();
    }
    return atLeast(revision);
  }

  /**
   * Returns {@link #atLeast(ZedToken)} if revision is non-null and non-empty, otherwise {@link
   * #minLatency()}. Use this when you have an optional revision but prefer performance over full
   * consistency as the fallback.
   *
   * @param revision an opaque ZedToken value, or null/empty
   */
  public static Consistency atLeastOrMinLatency(ZedToken revision) {
    if (revision == null || revision.value().isEmpty()) {
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
