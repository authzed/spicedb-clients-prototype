package com.authzed.spicedb;

import build.buf.gen.authzed.api.v1.ZedToken;

/**
 * Consistency strategies for SpiceDB read operations.
 *
 * <p>Every read operation requires an explicit consistency strategy. This prevents
 * silent defaults and makes consistency choices visible in the calling code.
 *
 * <p>ZedTokens are represented as opaque {@code String} values, never proto types.
 */
public final class Consistency {

    private final build.buf.gen.authzed.api.v1.Consistency proto;

    private Consistency(build.buf.gen.authzed.api.v1.Consistency proto) {
        this.proto = proto;
    }

    /**
     * Returns the underlying proto Consistency for use by the client.
     * This is exposed for advanced use cases only.
     */
    public build.buf.gen.authzed.api.v1.Consistency toProto() {
        return proto;
    }

    /**
     * Returns a strategy that requires full consistency. This is the least
     * performant option but guarantees the most up-to-date results.
     */
    public static Consistency full() {
        return new Consistency(
            build.buf.gen.authzed.api.v1.Consistency.newBuilder()
                .setFullyConsistent(true)
                .build()
        );
    }

    /**
     * Returns a strategy that uses SpiceDB's preferred revision for optimal
     * performance. This is the recommended default for most read operations.
     */
    public static Consistency minLatency() {
        return new Consistency(
            build.buf.gen.authzed.api.v1.Consistency.newBuilder()
                .setMinimizeLatency(true)
                .build()
        );
    }

    /**
     * Returns a strategy that ensures results are at least as fresh as the
     * given revision. Use this for read-after-write consistency.
     *
     * @param revision a ZedToken string from a previous write
     * @throws IllegalArgumentException if revision is null or empty
     */
    public static Consistency atLeast(String revision) {
        if (revision == null || revision.isEmpty()) {
            throw new IllegalArgumentException("revision must not be null or empty");
        }
        return new Consistency(
            build.buf.gen.authzed.api.v1.Consistency.newBuilder()
                .setAtLeastAsFresh(ZedToken.newBuilder().setToken(revision).build())
                .build()
        );
    }

    /**
     * Returns a strategy that reads at the exact given revision.
     *
     * @param revision a ZedToken string
     * @throws IllegalArgumentException if revision is null or empty
     */
    public static Consistency snapshot(String revision) {
        if (revision == null || revision.isEmpty()) {
            throw new IllegalArgumentException("revision must not be null or empty");
        }
        return new Consistency(
            build.buf.gen.authzed.api.v1.Consistency.newBuilder()
                .setAtExactSnapshot(ZedToken.newBuilder().setToken(revision).build())
                .build()
        );
    }

    /**
     * Returns {@link #atLeast(String)} if revision is non-null and non-empty,
     * otherwise {@link #full()}. Use this when you have an optional revision
     * from a previous write and want the safest fallback.
     *
     * @param revision a ZedToken string, or null/empty
     */
    public static Consistency atLeastOrFull(String revision) {
        if (revision == null || revision.isEmpty()) {
            return full();
        }
        return atLeast(revision);
    }

    /**
     * Returns {@link #atLeast(String)} if revision is non-null and non-empty,
     * otherwise {@link #minLatency()}. Use this when you have an optional
     * revision but prefer performance over full consistency as the fallback.
     *
     * @param revision a ZedToken string, or null/empty
     */
    public static Consistency atLeastOrMinLatency(String revision) {
        if (revision == null || revision.isEmpty()) {
            return minLatency();
        }
        return atLeast(revision);
    }

    private Consistency() {
        throw new UnsupportedOperationException();
    }
}
