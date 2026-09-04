package com.authzed.spicedb;

import java.time.Duration;
import java.util.Map;

/**
 * Immutable call-level options for the permission-check operations.
 *
 * <p>This type exists so that a new check option is a new component here rather than a new
 * overload. Before it, {@code checkPermission} had one overload taking a {@link Duration} and
 * another taking a context {@link Map} — which meant a caller could set either, and <em>neither
 * combination of the two</em>, because no overload accepted both. Every further option would have
 * multiplied that. See root DESIGN.md, "RULE: Every RPC wrapper must have one place to add an
 * option".
 *
 * <pre>{@code
 * CheckOptions options = CheckOptions.none()
 *     .withContext(Map.of("now", 42))
 *     .withTimeout(Duration.ofSeconds(2));
 * }</pre>
 *
 * @param context call-level caveat context, applied to every relationship the call evaluates and
 *     merged key-by-key with each relationship's own context, where the relationship's own value
 *     wins for keys present in both. Null sends no context.
 * @param timeout deadline for this call, overriding the client default. Null applies the client
 *     default rather than removing the bound — see root DESIGN.md, "RULE: A unary call must have a
 *     deadline".
 */
public record CheckOptions(Map<String, Object> context, Duration timeout) {

  /** Options with nothing set: the client's defaults apply. */
  public static CheckOptions none() {
    return new CheckOptions(null, null);
  }

  /** Returns a copy with the given call-level caveat context. */
  public CheckOptions withContext(Map<String, Object> context) {
    return new CheckOptions(context, timeout);
  }

  /** Returns a copy with the given per-call deadline. */
  public CheckOptions withTimeout(Duration timeout) {
    return new CheckOptions(context, timeout);
  }
}
