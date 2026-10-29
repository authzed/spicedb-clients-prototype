package com.authzed.spicedb;

/**
 * Checks the protobuf-java runtime on the class path against the version the proto tier's classes
 * were generated with.
 *
 * <p>Generated protobuf classes refuse a runtime older than their gencode, but only when a message
 * class first loads, which is usually the first call. A dependency manager that pins an older
 * protobuf-java (a framework BOM, for example) then passes the build and fails in production. Every
 * client constructor runs this check first, so the mismatch fails at startup with the property to
 * override.
 */
final class ProtobufRuntime {
  /**
   * The protobuf-java gencode version of the proto tier (its {@code buf.gen.yaml} pins protoc
   * v33.2). A test keeps it equal to the version stamped in the generated sources.
   */
  static final int[] FLOOR = {4, 33, 2};

  private static volatile boolean verified;

  private ProtobufRuntime() {}

  /** Throws {@link IllegalStateException} when the protobuf-java runtime is older than FLOOR. */
  static void requireSupported() {
    if (verified) return;
    requireAtLeast(detect());
    verified = true;
  }

  /**
   * The runtime's version as {@code {major, minor, patch}}, or {@code null} when it predates
   * protobuf-java 4.26 (which added {@code com.google.protobuf.RuntimeVersion}).
   *
   * <p>Read reflectively: {@code RuntimeVersion.MAJOR} and friends are compile-time constants, so a
   * direct reference would be inlined with the version this library was compiled against.
   */
  static int[] detect() {
    try {
      Class<?> rv = Class.forName("com.google.protobuf.RuntimeVersion");
      return new int[] {
        rv.getField("MAJOR").getInt(null),
        rv.getField("MINOR").getInt(null),
        rv.getField("PATCH").getInt(null)
      };
    } catch (ClassNotFoundException e) {
      return null;
    } catch (ReflectiveOperationException | RuntimeException e) {
      // An unexpected RuntimeVersion shape: leave the decision to the generated classes' own check.
      return FLOOR.clone();
    }
  }

  /** Throws when {@code runtime} (null meaning older than 4.26) is below FLOOR. */
  static void requireAtLeast(int[] runtime) {
    if (runtime != null && compare(runtime, FLOOR) >= 0) return;
    String found = runtime == null ? "a version older than 4.26" : format(runtime);
    throw new IllegalStateException(
        "spicedb-java needs protobuf-java "
            + format(FLOOR)
            + " or later at run time, but the class path has "
            + found
            + ". A dependency manager is probably pinning an older version: in Maven or Spring"
            + " Boot, set the property <protobuf-java.version>"
            + format(FLOOR)
            + "</protobuf-java.version> (or later); in Gradle, require"
            + " com.google.protobuf:protobuf-java:"
            + format(FLOOR)
            + " or later.");
  }

  private static int compare(int[] a, int[] b) {
    for (int i = 0; i < 3; i++) if (a[i] != b[i]) return Integer.compare(a[i], b[i]);
    return 0;
  }

  static String format(int[] v) {
    return v[0] + "." + v[1] + "." + v[2];
  }
}
