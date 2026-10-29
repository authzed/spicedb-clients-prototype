package com.authzed.spicedb;

/**
 * Whether a bounded {@code deleteRelationships} may delete part of the matches. {@code DISALLOWED}
 * is all-or-nothing: the server rejects a delete that exceeds its limit instead of deleting part of
 * it, and the limit may be omitted to use the server's default.
 */
public enum PartialDeletion {
  ALLOWED,
  DISALLOWED
}
