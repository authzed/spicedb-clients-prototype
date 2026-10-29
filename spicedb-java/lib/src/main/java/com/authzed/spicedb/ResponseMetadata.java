package com.authzed.spicedb;

import io.grpc.Metadata;

/**
 * Captures gRPC response headers and trailers for one operation (see {@link
 * TransportOptions.Builder#responseMetadata}). This is the supported route to transport-level data,
 * including what SpiceDB servers older than 1.30 send only in trailers.
 *
 * <p>An operation that makes several calls (retries, bulk-check chunks, pages) records the most
 * recent call's metadata. Values are {@code null} until a call has received them. Thread-safe.
 */
public final class ResponseMetadata {
  private volatile Metadata headers;
  private volatile Metadata trailers;

  /** The latest response headers, or {@code null}. */
  public Metadata headers() {
    return headers;
  }

  /** The latest response trailers, or {@code null}. */
  public Metadata trailers() {
    return trailers;
  }

  /**
   * Records response headers. The client calls this for every call; test doubles and wrappers call
   * it to report metadata as the real client would.
   */
  public void recordHeaders(Metadata value) {
    headers = value;
  }

  /** Records response trailers; see {@link #recordHeaders}. */
  public void recordTrailers(Metadata value) {
    trailers = value;
  }
}
