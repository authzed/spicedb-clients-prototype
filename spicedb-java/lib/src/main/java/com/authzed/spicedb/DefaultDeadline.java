package com.authzed.spicedb;

/**
 * Whether unary operations without their own {@code timeout} get the client's default deadline
 * ({@link ClientOptions#defaultTimeout()}, 30 seconds unless changed). {@code DISABLED} leaves such
 * calls unbounded except for the caller's own gRPC {@code Context} deadline or cancellation.
 */
public enum DefaultDeadline {
  ENABLED,
  DISABLED
}
