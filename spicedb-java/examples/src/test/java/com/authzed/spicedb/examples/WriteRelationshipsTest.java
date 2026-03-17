package com.authzed.spicedb.examples;

import com.authzed.spicedb.Filter;
import com.authzed.spicedb.Relationship;
import com.authzed.spicedb.SpiceDBClient;
import com.authzed.spicedb.Transaction;

import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import static com.authzed.spicedb.Consistency.*;
import static org.assertj.core.api.Assertions.*;

/**
 * Demonstrates writing relationships with the {@link Transaction} builder,
 * including touch, create, delete, and preconditions.
 */
class WriteRelationshipsTest {

    private SpiceDBClient client;

    @BeforeEach
    void setUp() {
        client = SpiceDBClient.createPlaintext("localhost:50051", "somerandomkeyhere");

        client.writeSchema("""
            definition user {}

            definition document {
                relation viewer: user
                relation editor: user
                relation owner: user
                permission view = viewer + editor + owner
                permission edit = editor + owner
                permission delete = owner
            }""");
    }

    @AfterEach
    void tearDown() {
        client.close();
    }

    @Test
    void touch_creates_relationships_and_returns_revision() {
        var txn = new Transaction();
        txn.touch(Relationship.of("document", "firstdoc", "viewer", "user", "alice"));
        txn.touch(Relationship.of("document", "firstdoc", "editor", "user", "bob"));

        String revision = client.write(txn);

        assertThat(revision).isNotEmpty();
    }

    @Test
    void precondition_mustNotMatch_succeeds_when_no_match() {
        var txn = new Transaction();
        txn.touch(Relationship.of("document", "firstdoc", "viewer", "user", "alice"));
        txn.mustNotMatch(Filter.of("document")
            .withResourceID("firstdoc")
            .withRelation("owner")
            .withSubjectType("user")
            .withSubjectID("mallory"));

        String revision = client.write(txn);

        assertThat(revision).isNotEmpty();
    }

    @Test
    void delete_removes_relationship() {
        // First create
        var create = new Transaction();
        create.touch(Relationship.of("document", "deldoc", "viewer", "user", "charlie"));
        client.write(create);

        // Then delete
        var del = new Transaction();
        del.delete(Relationship.of("document", "deldoc", "viewer", "user", "charlie"));
        String revision = client.write(del);

        assertThat(revision).isNotEmpty();

        // Verify it's gone
        long count = client.readRelationships(full(),
            Filter.of("document").withResourceID("deldoc").withRelation("viewer")).count();
        assertThat(count).isZero();
    }
}
