package com.authzed.spicedb;

import static org.junit.jupiter.api.Assertions.*;

import org.junit.jupiter.api.Test;

class ConsistencyTest {

  @Test
  void fullReturnsFullConsistency() {
    Consistency c = Consistency.full();
    assertNotNull(c.toProto());
    assertTrue(c.toProto().getFullyConsistent());
  }

  @Test
  void minLatencyReturnsMinimizeLatency() {
    Consistency c = Consistency.minLatency();
    assertNotNull(c.toProto());
    assertTrue(c.toProto().getMinimizeLatency());
  }

  @Test
  void atLeastReturnsAtLeastAsFresh() {
    Consistency c = Consistency.atLeast(new com.authzed.spicedb.ZedToken("token123"));
    assertNotNull(c.toProto());
    assertEquals("token123", c.toProto().getAtLeastAsFresh().getToken());
  }

  @Test
  void atLeastRejectsNullRevision() {
    assertThrows(
        com.authzed.spicedb.errors.InvalidArgumentException.class, () -> Consistency.atLeast(null));
  }

  @Test
  void atLeastRejectsEmptyRevision() {
    assertThrows(
        com.authzed.spicedb.errors.InvalidArgumentException.class,
        () -> Consistency.atLeast(new com.authzed.spicedb.ZedToken("")));
  }

  @Test
  void snapshotReturnsExactSnapshot() {
    Consistency c = Consistency.snapshot(new com.authzed.spicedb.ZedToken("snap456"));
    assertNotNull(c.toProto());
    assertEquals("snap456", c.toProto().getAtExactSnapshot().getToken());
  }

  @Test
  void snapshotRejectsNullRevision() {
    assertThrows(
        com.authzed.spicedb.errors.InvalidArgumentException.class,
        () -> Consistency.snapshot(null));
  }

  @Test
  void snapshotRejectsEmptyRevision() {
    assertThrows(
        com.authzed.spicedb.errors.InvalidArgumentException.class,
        () -> Consistency.snapshot(new com.authzed.spicedb.ZedToken("")));
  }

  @Test
  void atLeastOrFullReturnsFullWhenEmpty() {
    Consistency c = Consistency.atLeastOrFull(new com.authzed.spicedb.ZedToken(""));
    assertTrue(c.toProto().getFullyConsistent());
  }

  @Test
  void atLeastOrFullReturnsFullWhenNull() {
    Consistency c = Consistency.atLeastOrFull(null);
    assertTrue(c.toProto().getFullyConsistent());
  }

  @Test
  void atLeastOrFullReturnsAtLeastWhenPresent() {
    Consistency c = Consistency.atLeastOrFull(new com.authzed.spicedb.ZedToken("rev1"));
    assertEquals("rev1", c.toProto().getAtLeastAsFresh().getToken());
  }

  @Test
  void atLeastOrMinLatencyReturnsMinLatencyWhenEmpty() {
    Consistency c = Consistency.atLeastOrMinLatency(new com.authzed.spicedb.ZedToken(""));
    assertTrue(c.toProto().getMinimizeLatency());
  }

  @Test
  void atLeastOrMinLatencyReturnsMinLatencyWhenNull() {
    Consistency c = Consistency.atLeastOrMinLatency(null);
    assertTrue(c.toProto().getMinimizeLatency());
  }

  @Test
  void atLeastOrMinLatencyReturnsAtLeastWhenPresent() {
    Consistency c = Consistency.atLeastOrMinLatency(new com.authzed.spicedb.ZedToken("rev2"));
    assertEquals("rev2", c.toProto().getAtLeastAsFresh().getToken());
  }

  @Test
  void strictStrategiesRejectWhitespaceOnlyRevisionLikeEmpty() {
    for (var blank : java.util.List.of(" ", "\t", " \n ")) {
      var token = new com.authzed.spicedb.ZedToken(blank);
      var atLeast =
          assertThrows(
              com.authzed.spicedb.errors.LocalValidationException.class,
              () -> Consistency.atLeast(token));
      assertTrue(atLeast.getMessage().contains("whitespace-only"), atLeast.getMessage());
      assertThrows(
          com.authzed.spicedb.errors.LocalValidationException.class,
          () -> Consistency.snapshot(token));
    }
  }

  @Test
  void fallbackStrategiesTreatWhitespaceOnlyRevisionAsAbsent() {
    var token = new com.authzed.spicedb.ZedToken("  ");
    assertTrue(Consistency.atLeastOrFull(token).toProto().getFullyConsistent());
    assertTrue(Consistency.atLeastOrMinLatency(token).toProto().getMinimizeLatency());
  }
}
