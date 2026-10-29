package com.authzed.spicedb;

/**
 * Whether opening a stream (read, lookup, export, materialize lookup) is retried before any item is
 * exposed. A partially consumed stream is never restarted. Watches are never reopened by the
 * client-wide policy whatever this says; only a per-call or per-operation retry policy reopens one.
 */
public enum StreamRetries {
  ENABLED,
  DISABLED
}
