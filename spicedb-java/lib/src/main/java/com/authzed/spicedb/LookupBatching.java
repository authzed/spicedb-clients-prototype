package com.authzed.spicedb;

/** Whether {@code lookupResources} asks the server to batch resources into response messages. */
public enum LookupBatching {
  /**
   * Request batched responses (batch size from the options). Results are still yielded one at a
   * time, and only the last one yielded from each batch carries an after-result cursor. A server
   * that does not batch answers one resource per message, and then every item carries its cursor.
   * The default.
   */
  AUTOMATIC,
  /** Request one resource per response message, so every item carries its after-result cursor. */
  DISABLED
}
