package com.authzed.spicedb.examples;

import com.authzed.spicedb.Filter;
import com.authzed.spicedb.Relationship;
import com.authzed.spicedb.Transaction;

import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import java.util.List;

import static com.authzed.spicedb.Consistency.*;
import static org.assertj.core.api.Assertions.*;

/**
 * Demonstrates reading relationships using {@link com.authzed.spicedb.SpiceDBClient#readRelationships}
 * with cursor-based auto-pagination.
 */
class ReadRelationshipsTest extends SpiceDBIntegrationTest {

    @BeforeEach
    void setUp() {
        client.deleteRelationships(Filter.of("document"));

        var txn = new Transaction();
        txn.touch(Relationship.of("document", "firstdoc", "viewer", "user", "alice"));
        txn.touch(Relationship.of("document", "firstdoc", "viewer", "user", "bob"));
        txn.touch(Relationship.of("document", "firstdoc", "editor", "user", "charlie"));
        client.write(txn);
    }

    @Test
    void reads_viewers_of_document() {
        Filter filter = Filter.of("document")
            .withResourceID("firstdoc")
            .withRelation("viewer");

        List<Relationship> relationships;
        try (var stream = client.readRelationships(full(), filter)) {
            relationships = stream.toList();
        }

        assertThat(relationships).hasSize(2);
        assertThat(relationships)
            .extracting(Relationship::subjectID)
            .containsExactlyInAnyOrder("alice", "bob");
    }

    @Test
    void reads_all_relations_on_document() {
        Filter filter = Filter.of("document")
            .withResourceID("firstdoc");

        List<Relationship> relationships;
        try (var stream = client.readRelationships(full(), filter)) {
            relationships = stream.toList();
        }

        assertThat(relationships).hasSize(3);
        assertThat(relationships)
            .extracting(Relationship::resourceRelation)
            .contains("viewer", "editor");
    }

    @Test
    void empty_result_for_nonexistent_resource() {
        Filter filter = Filter.of("document")
            .withResourceID("nonexistent");

        try (var stream = client.readRelationships(full(), filter)) {
            assertThat(stream.count()).isZero();
        }
    }
}
