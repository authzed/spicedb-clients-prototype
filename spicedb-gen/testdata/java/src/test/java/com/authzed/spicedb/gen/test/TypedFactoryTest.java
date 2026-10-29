package com.authzed.spicedb.gen.test;

import static com.authzed.spicedb.gen.test.Permissions.*;
import static org.junit.jupiter.api.Assertions.*;

import com.authzed.spicedb.ClientOperation;
import com.authzed.spicedb.ClientOptions;
import com.authzed.spicedb.PartialDeletion;
import com.authzed.spicedb.testing.InMemorySpiceDB;
import java.util.List;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicInteger;
import org.junit.jupiter.api.Test;

/** Typed factories with a channel customizer, and the filter-only delete forwarder. */
class TypedFactoryTest {

  @Test
  void factoriesPassTheChannelCustomizerThrough() {
    var calls = new AtomicInteger();
    var options = ClientOptions.builder().build();
    try (var plaintext =
        TypedClient.createPlaintext(
            "localhost:50051",
            "key",
            options,
            b -> {
              calls.incrementAndGet();
              b.keepAliveTime(60, TimeUnit.SECONDS).keepAliveTimeout(10, TimeUnit.SECONDS);
            })) {
      assertNotNull(plaintext.client());
    }
    try (var tls =
        TypedClient.createSystemTls(
            "spicedb.example.com:443",
            "key",
            options,
            b -> {
              calls.incrementAndGet();
              b.keepAliveTime(60, TimeUnit.SECONDS).keepAliveWithoutCalls(true);
            })) {
      assertNotNull(tls.client());
    }
    assertEquals(2, calls.get());
  }

  @Test
  void filterOnlyDeleteIsOneAllOrNothingRequest() {
    var db = new InMemorySpiceDB();
    try (var typed = new TypedClient(db)) {
      typed.touch(List.of(Document("readme").editor(User("alice"))));
      typed.deleteRelationships(Document("readme").filter());
    }
    assertTrue(db.relationships().isEmpty());
    var call = db.callsOf(ClientOperation.DELETE_RELATIONSHIPS).get(0);
    assertEquals(
        PartialDeletion.DISALLOWED,
        ((com.authzed.spicedb.DeleteOptions) call.options()).partialDeletion());
  }
}
