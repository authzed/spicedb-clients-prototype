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
            definition cp_user {}

            definition cp_document {
                relation viewer: cp_user
                relation editor: cp_user
                relation owner: cp_user
                permission view = viewer + editor + owner
                permission edit = editor + owner
                permission delete = owner
            }""");

        var txn = new Transaction();
        txn.touch(Relationship.of("cp_document", "firstdoc", "viewer", "cp_user", "alice"));
        txn.touch(Relationship.of("cp_document", "firstdoc", "editor", "cp_user", "bob"));
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
            Relationship.of("cp_document", "firstdoc", "view", "cp_user", "alice"));

        assertThat(allowed).isTrue();
    }

    @Test
    void alice_cannot_edit_document() {
        boolean allowed = client.checkPermission(
            full(), "edit",
            Relationship.of("cp_document", "firstdoc", "edit", "cp_user", "alice"));

        assertThat(allowed).isFalse();
    }

    @Test
    void bob_can_edit_and_view_document() {
        boolean canEdit = client.checkPermission(
            full(), "edit",
            Relationship.of("cp_document", "firstdoc", "edit", "cp_user", "bob"));
        boolean canView = client.checkPermission(
            full(), "view",
            Relationship.of("cp_document", "firstdoc", "view", "cp_user", "bob"));

        assertThat(canEdit).isTrue();
        assertThat(canView).isTrue();
    }
}
