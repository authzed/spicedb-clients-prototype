package com.authzed.spicedb;

import static org.junit.jupiter.api.Assertions.*;

import java.nio.file.Files;
import java.nio.file.Path;
import java.util.regex.Pattern;
import org.junit.jupiter.api.Test;

/** The grpc-java runtime floor is checked at client construction, naming what to override. */
class GrpcRuntimeTest {

  @Test
  void olderRuntimeFailsNamingTheMavenAndSpringBootProperties() {
    var e =
        assertThrows(
            IllegalStateException.class, () -> GrpcRuntime.requireAtLeast(new int[] {1, 68, 1}));
    assertTrue(e.getMessage().contains("<grpc.version>"), e.getMessage());
    assertTrue(e.getMessage().contains("<grpc-java.version>"), e.getMessage());
    assertTrue(e.getMessage().contains("grpc-bom"), e.getMessage());
    assertTrue(e.getMessage().contains("1.68.1"), e.getMessage());
    assertTrue(e.getMessage().contains(GrpcRuntime.format(GrpcRuntime.FLOOR)));
  }

  @Test
  void unreadableVersionSkipsTheCheck() {
    GrpcRuntime.requireAtLeast(null);
    assertNull(GrpcRuntime.parse(null));
    assertNull(GrpcRuntime.parse("unknown"));
  }

  @Test
  void parsesReleaseAndSnapshotVersions() {
    assertArrayEquals(new int[] {1, 80, 0}, GrpcRuntime.parse("1.80.0"));
    assertArrayEquals(new int[] {1, 85, 0}, GrpcRuntime.parse("1.85.0-SNAPSHOT"));
  }

  @Test
  void floorAndNewerRuntimesPass() {
    GrpcRuntime.requireAtLeast(GrpcRuntime.FLOOR.clone());
    GrpcRuntime.requireAtLeast(new int[] {1, 84, 0});
    GrpcRuntime.requireAtLeast(new int[] {2, 0, 0});
    GrpcRuntime.requireSupported();
  }

  @Test
  void detectsTheRuntimeOnTheClassPath() {
    int[] runtime = GrpcRuntime.detect();
    assertNotNull(runtime);
    assertEquals(1, runtime[0]);
  }

  /** FLOOR must be the protoc-gen-grpc-java version the proto tier's stubs were generated with. */
  @Test
  void floorMatchesTheStubGeneratorVersion() throws Exception {
    var gen = Path.of(System.getProperty("spicedb.protoGenDir"));
    var bufGen = Files.readString(gen.resolveSibling("buf.gen.yaml"));
    var m = Pattern.compile("buf\\.build/grpc/java:v(\\d+)\\.(\\d+)\\.(\\d+)").matcher(bufGen);
    assertTrue(m.find());
    assertArrayEquals(
        new int[] {
          Integer.parseInt(m.group(1)), Integer.parseInt(m.group(2)), Integer.parseInt(m.group(3))
        },
        GrpcRuntime.FLOOR);
  }
}
