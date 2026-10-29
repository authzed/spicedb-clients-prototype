package com.authzed.spicedb;

/**
 * Whether the client retries idempotent reads per its {@link RetryPolicy}. {@code DISABLED} is for
 * callers that apply their own policy; mutations are never retried in either mode.
 */
public enum Retries {
  ENABLED,
  DISABLED
}
