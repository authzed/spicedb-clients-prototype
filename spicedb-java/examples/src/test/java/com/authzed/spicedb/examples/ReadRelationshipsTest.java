package com.authzed.spicedb.examples;

import com.authzed.spicedb.Filter;
import com.authzed.spicedb.Relationship;
import com.authzed.spicedb.SpiceDBClient;
import com.authzed.spicedb.Transaction;

import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import java.util.List;

import static com.authzed.spicedb.Consistency.*;
import static org.assertj.core.api.Assertions.*;

/**
 * Demonstrates reading relationships using {@link SpiceDBClient#readRelationships}
 * with cursor-based auto-pagination.
 */
class ReadRelationshipsTest {

    private SpiceDBClient client;

    @BeforeEach
    void setUp() {
        client = SpiceDBClient.createPlaintext("localhost:50051", "somerandomkeyhere");

        client.writeSchema("""
            definition rr_user {}

            definition rr_document {
                relation viewer: rr_user
                relation editor: rr_user
                relation owner: rr_user
                permission view = viewer + editor + owner
                permission edit = editor + owner
                permission delete = owner
            }""");

        var txn = new Transaction();
        txn.touch(Relationship.of("rr_document", "firstdoc", "viewer", "rr_user", "alice"));
        txn.touch(Relationship.of("rr_document", "firstdoc", "viewer", "rr_user", "bob"));
        txn.touch(Relationship.of("rr_document", "firstdoc", "editor", "rr_user", "charlie"));
        client.write(txn);
    }

    @AfterEach
    void tearDown() {
        client.close();
    }

    @Test
    void reads_viewers_of_document() {
        Filter filter = Filter.of("rr_document")
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
        Filter filter = Filter.of("rr_document")
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
        Filter filter = Filter.of("rr_document")
            .withResourceID("nonexistent");

        try (var stream = client.readRelationships(full(), filter)) {
            assertThat(stream.count()).isZero();
        }
    }
}
