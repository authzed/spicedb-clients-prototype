package com.authzed.spicedb.examples;

import com.authzed.spicedb.Relationship;
import com.authzed.spicedb.SpiceDBClient;
import com.authzed.spicedb.Transaction;

import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import static com.authzed.spicedb.Consistency.*;
import static org.assertj.core.api.Assertions.*;

/**
 * Demonstrates checking a single permission using {@link SpiceDBClient#checkPermission}.
 */
class CheckPermissionTest {

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

        var txn = new Transaction();
        txn.touch(Relationship.of("document", "firstdoc", "viewer", "user", "alice"));
        txn.touch(Relationship.of("document", "firstdoc", "editor", "user", "bob"));
        client.write(txn);
    }

    @AfterEach
    void tearDown() {
        client.close();
    }

    @Test
    void alice_can_view_document() {
        boolean allowed = client.checkPermission(
            full(), "view",
            Relationship.of("document", "firstdoc", "view", "user", "alice"));

        assertThat(allowed).isTrue();
    }

    @Test
    void alice_cannot_edit_document() {
        boolean allowed = client.checkPermission(
            full(), "edit",
            Relationship.of("document", "firstdoc", "edit", "user", "alice"));

        assertThat(allowed).isFalse();
    }

    @Test
    void bob_can_edit_and_view_document() {
        boolean canEdit = client.checkPermission(
            full(), "edit",
            Relationship.of("document", "firstdoc", "edit", "user", "bob"));
        boolean canView = client.checkPermission(
            full(), "view",
            Relationship.of("document", "firstdoc", "view", "user", "bob"));

        assertThat(canEdit).isTrue();
        assertThat(canView).isTrue();
    }
}
