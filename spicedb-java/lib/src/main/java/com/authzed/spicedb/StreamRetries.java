package com.authzed.spicedb;

/**
 * Whether opening a stream (read, lookup, export, watch, materialize) is retried before any item is
 * exposed. A partially consumed stream is never restarted. Long-lived streams such as watch may opt
 * out.
 */
public enum StreamRetries {
  ENABLED,
  DISABLED
}
