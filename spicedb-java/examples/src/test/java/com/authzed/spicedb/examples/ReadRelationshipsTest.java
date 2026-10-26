package com.authzed.spicedb.examples;

import static com.authzed.spicedb.Consistency.*;
import static org.assertj.core.api.Assertions.*;

import com.authzed.spicedb.*;
import com.authzed.spicedb.Filter;
import com.authzed.spicedb.Relationship;
import com.authzed.spicedb.Transaction;
import java.util.List;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

/**
 * Demonstrates reading relationships using {@link
 * com.authzed.spicedb.SpiceDBClient#readRelationships} with cursor-based auto-pagination.
 */
class ReadRelationshipsTest extends SpiceDBIntegrationTest {

  @BeforeEach
  void setUp() {
    client.deleteAllRelationshipsNonAtomic(Filter.of("document"));

    var txn = new Transaction();
    txn.touch(
        Relationship.of(
            new ObjectRef("document", "firstdoc"),
            "viewer",
            SubjectRef.direct(new ObjectRef("user", "alice"))));
    txn.touch(
        Relationship.of(
            new ObjectRef("document", "firstdoc"),
            "viewer",
            SubjectRef.direct(new ObjectRef("user", "bob"))));
    txn.touch(
        Relationship.of(
            new ObjectRef("document", "firstdoc"),
            "editor",
            SubjectRef.direct(new ObjectRef("user", "charlie"))));
    client.write(txn);
  }

  @Test
  void reads_viewers_of_document() {
    Filter filter = Filter.of("document").withResourceId("firstdoc").withRelation("viewer");

    List<Relationship> relationships;
    try (var stream = client.readRelationships(full(), filter)) {
      relationships = stream.toList();
    }

    assertThat(relationships).hasSize(2);
    assertThat(relationships)
        .extracting(r -> r.subject().object().objectId())
        .containsExactlyInAnyOrder("alice", "bob");
  }

  @Test
  void reads_all_relations_on_document() {
    Filter filter = Filter.of("document").withResourceId("firstdoc");

    List<Relationship> relationships;
    try (var stream = client.readRelationships(full(), filter)) {
      relationships = stream.toList();
    }

    assertThat(relationships).hasSize(3);
    assertThat(relationships).extracting(Relationship::relation).contains("viewer", "editor");
  }

  @Test
  void empty_result_for_nonexistent_resource() {
    Filter filter = Filter.of("document").withResourceId("nonexistent");

    try (var stream = client.readRelationships(full(), filter)) {
      assertThat(stream.count()).isZero();
    }
  }
}
