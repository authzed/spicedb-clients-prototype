package com.authzed.spicedb;

import static org.junit.jupiter.api.Assertions.*;

import java.nio.file.Files;
import java.nio.file.Path;
import java.util.regex.Pattern;
import org.junit.jupiter.api.Test;

/** The protobuf-java runtime floor is checked at client construction, naming what to override. */
class ProtobufRuntimeTest {

  @Test
  void olderRuntimeFailsNamingTheMavenProperty() {
    var e =
        assertThrows(
            IllegalStateException.class,
            () -> ProtobufRuntime.requireAtLeast(new int[] {4, 28, 3}));
    assertTrue(e.getMessage().contains("protobuf-java.version"), e.getMessage());
    assertTrue(e.getMessage().contains("4.28.3"), e.getMessage());
    assertTrue(e.getMessage().contains(ProtobufRuntime.format(ProtobufRuntime.FLOOR)));
  }

  @Test
  void runtimeWithoutVersionClassFails() {
    var e = assertThrows(IllegalStateException.class, () -> ProtobufRuntime.requireAtLeast(null));
    assertTrue(e.getMessage().contains("older than 4.26"), e.getMessage());
  }

  @Test
  void floorAndNewerRuntimesPass() {
    ProtobufRuntime.requireAtLeast(ProtobufRuntime.FLOOR.clone());
    ProtobufRuntime.requireAtLeast(new int[] {4, 34, 2});
    ProtobufRuntime.requireAtLeast(new int[] {4, 36, 0});
    ProtobufRuntime.requireSupported();
  }

  @Test
  void detectsTheRuntimeOnTheClassPath() {
    int[] runtime = ProtobufRuntime.detect();
    assertNotNull(runtime);
    assertEquals(com.google.protobuf.RuntimeVersion.MAJOR, runtime[0]);
  }

  /** FLOOR must be the gencode version the proto tier's classes were generated with. */
  @Test
  void floorMatchesTheGencodeVersion() throws Exception {
    var gen = Path.of(System.getProperty("spicedb.protoGenDir"));
    var source = Files.readString(gen.resolve("com/authzed/api/v1/CheckPermissionRequest.java"));
    var m = Pattern.compile("Protobuf Java Version: (\\d+)\\.(\\d+)\\.(\\d+)").matcher(source);
    assertTrue(m.find());
    assertArrayEquals(
        new int[] {
          Integer.parseInt(m.group(1)), Integer.parseInt(m.group(2)), Integer.parseInt(m.group(3))
        },
        ProtobufRuntime.FLOOR);
  }
}
