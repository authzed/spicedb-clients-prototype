package com.authzed.spicedb;

import java.util.regex.Matcher;
import java.util.regex.Pattern;

/**
 * Checks the grpc-java runtime on the class path against the version the proto tier's service stubs
 * were generated with.
 *
 * <p>An older grpc-java does not fail at build time: a dependency manager that pins one (a
 * framework BOM, for example) passes the build, and the stubs or the client then fail at the first
 * call that reaches an API the old runtime lacks, often with a {@code NoSuchMethodError}. Every
 * client constructor runs this check beside {@link ProtobufRuntime}, so the mismatch fails at
 * startup with the property to override. When the version cannot be read (a repackaged jar without
 * its manifest, for example) the check is skipped.
 */
final class GrpcRuntime {
  /**
   * The protoc-gen-grpc-java version of the proto tier's stubs (its {@code buf.gen.yaml} pins
   * {@code buf.build/grpc/java:v1.80.0}). A test keeps it equal to that pin.
   */
  static final int[] FLOOR = {1, 80, 0};

  private static final Pattern VERSION = Pattern.compile("^(\\d+)\\.(\\d+)\\.(\\d+)");

  private static volatile boolean verified;

  private GrpcRuntime() {}

  /** Throws {@link IllegalStateException} when the grpc-java runtime is older than FLOOR. */
  static void requireSupported() {
    if (verified) return;
    requireAtLeast(detect());
    verified = true;
  }

  /**
   * The runtime's version as {@code {major, minor, patch}}, or {@code null} when it cannot be read.
   *
   * <p>Reads the {@code Implementation-Version} of grpc-api's manifest (the package of {@code
   * io.grpc.ManagedChannel}), then falls back to {@code io.grpc.internal.GrpcUtil
   * .IMPLEMENTATION_VERSION}, read reflectively because it is a compile-time constant that a direct
   * reference would inline with the version this library was compiled against.
   */
  static int[] detect() {
    Package pkg = io.grpc.ManagedChannel.class.getPackage();
    int[] v = parse(pkg == null ? null : pkg.getImplementationVersion());
    if (v != null) return v;
    try {
      Object field =
          Class.forName("io.grpc.internal.GrpcUtil").getField("IMPLEMENTATION_VERSION").get(null);
      return parse(field instanceof String s ? s : null);
    } catch (ReflectiveOperationException | RuntimeException | LinkageError e) {
      return null;
    }
  }

  /** {@code "1.80.0"} (or {@code "1.80.0-SNAPSHOT"}) as {@code {1, 80, 0}}; otherwise null. */
  static int[] parse(String version) {
    if (version == null) return null;
    Matcher m = VERSION.matcher(version);
    if (!m.find()) return null;
    try {
      return new int[] {
        Integer.parseInt(m.group(1)), Integer.parseInt(m.group(2)), Integer.parseInt(m.group(3))
      };
    } catch (NumberFormatException e) {
      return null;
    }
  }

  /** Throws when {@code runtime} is below FLOOR; a null (unreadable) version passes. */
  static void requireAtLeast(int[] runtime) {
    if (runtime == null || compare(runtime, FLOOR) >= 0) return;
    throw new IllegalStateException(
        "spicedb-java needs grpc-java "
            + format(FLOOR)
            + " or later at run time, but the class path has "
            + format(runtime)
            + ". A dependency manager is probably pinning an older version: in Maven or Spring"
            + " Boot, set the property <grpc.version>"
            + format(FLOOR)
            + "</grpc.version> (or later), or import io.grpc:grpc-bom at that version; in Gradle,"
            + " use platform(\"io.grpc:grpc-bom:"
            + format(FLOOR)
            + "\") or later. Keep every io.grpc artifact at one version.");
  }

  private static int compare(int[] a, int[] b) {
    for (int i = 0; i < 3; i++) if (a[i] != b[i]) return Integer.compare(a[i], b[i]);
    return 0;
  }

  static String format(int[] v) {
    return v[0] + "." + v[1] + "." + v[2];
  }
}
