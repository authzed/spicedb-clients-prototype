package com.authzed.spicedb;

/**
 * Immutable call-level options for the lookup operations.
 *
 * <p>This type exists so that a new lookup option is a new component here rather than a new
 * overload. {@code with_debug} arrived upstream with nowhere to go and became a second {@code
 * lookupResources} overload; the option after it would have needed a third. See root DESIGN.md,
 * "RULE: Every RPC wrapper must have one place to add an option".
 *
 * @param debug asks the server to attach debug information to the error raised when the lookup
 *     fails by exceeding the maximum dispatch depth. It has no effect on a successful call, and
 *     none on any other failure.
 */
public record LookupOptions(boolean debug) {

  /** Options with nothing set: the client's defaults apply. */
  public static LookupOptions none() {
    return new LookupOptions(false);
  }

  /** Returns a copy asking the server for debug information on a depth failure. */
  public LookupOptions withDebug(boolean debug) {
    return new LookupOptions(debug);
  }
}
