package com.authzed.spicedb;

/** How {@code readRelationships} and {@code lookupResources} fetch results. */
public enum Paging {
  /**
   * Fetch page after page (page size from the options) until results or the limit run out. The
   * default.
   */
  AUTOMATIC,
  /**
   * Send exactly one request, as the raw API does: its limit is the options' {@code limit} (or
   * unbounded without one), the page size is ignored, and no follow-up page is requested. Each item
   * still carries its after-result cursor.
   */
  SINGLE_REQUEST
}
