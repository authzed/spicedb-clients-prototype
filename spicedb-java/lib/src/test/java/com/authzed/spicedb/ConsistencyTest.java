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
    Consistency c = Consistency.atLeast("token123");
    assertNotNull(c.toProto());
    assertEquals("token123", c.toProto().getAtLeastAsFresh().getToken());
  }

  @Test
  void atLeastRejectsNullRevision() {
    assertThrows(IllegalArgumentException.class, () -> Consistency.atLeast(null));
  }

  @Test
  void atLeastRejectsEmptyRevision() {
    assertThrows(IllegalArgumentException.class, () -> Consistency.atLeast(""));
  }

  @Test
  void snapshotReturnsExactSnapshot() {
    Consistency c = Consistency.snapshot("snap456");
    assertNotNull(c.toProto());
    assertEquals("snap456", c.toProto().getAtExactSnapshot().getToken());
  }

  @Test
  void snapshotRejectsNullRevision() {
    assertThrows(IllegalArgumentException.class, () -> Consistency.snapshot(null));
  }

  @Test
  void snapshotRejectsEmptyRevision() {
    assertThrows(IllegalArgumentException.class, () -> Consistency.snapshot(""));
  }

  @Test
  void atLeastOrFullReturnsFullWhenEmpty() {
    Consistency c = Consistency.atLeastOrFull("");
    assertTrue(c.toProto().getFullyConsistent());
  }

  @Test
  void atLeastOrFullReturnsFullWhenNull() {
    Consistency c = Consistency.atLeastOrFull(null);
    assertTrue(c.toProto().getFullyConsistent());
  }

  @Test
  void atLeastOrFullReturnsAtLeastWhenPresent() {
    Consistency c = Consistency.atLeastOrFull("rev1");
    assertEquals("rev1", c.toProto().getAtLeastAsFresh().getToken());
  }

  @Test
  void atLeastOrMinLatencyReturnsMinLatencyWhenEmpty() {
    Consistency c = Consistency.atLeastOrMinLatency("");
    assertTrue(c.toProto().getMinimizeLatency());
  }

  @Test
  void atLeastOrMinLatencyReturnsMinLatencyWhenNull() {
    Consistency c = Consistency.atLeastOrMinLatency(null);
    assertTrue(c.toProto().getMinimizeLatency());
  }

  @Test
  void atLeastOrMinLatencyReturnsAtLeastWhenPresent() {
    Consistency c = Consistency.atLeastOrMinLatency("rev2");
    assertEquals("rev2", c.toProto().getAtLeastAsFresh().getToken());
  }
}
