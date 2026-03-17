package com.authzed.spicedb.examples;

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
 * Demonstrates bulk permission checks using {@link SpiceDBClient#checkPermissions},
 * {@link SpiceDBClient#checkAll}, and {@link SpiceDBClient#checkAny}.
 */
class BulkOperationsTest {

    private SpiceDBClient client;
    private String writeRevision;

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

        // Bulk write relationships
        var txn = new Transaction();
        for (String user : List.of("alice", "bob", "charlie")) {
            txn.touch(Relationship.of("document", "report", "viewer", "user", user));
        }
        txn.touch(Relationship.of("document", "report", "editor", "user", "alice"));
        writeRevision = client.write(txn);
    }

    @AfterEach
    void tearDown() {
        client.close();
    }

    @Test
    void bulk_check_returns_result_per_relationship() {
        List<Boolean> results = client.checkPermissions(
            atLeast(writeRevision), "view",
            Relationship.of("document", "report", "view", "user", "alice"),
            Relationship.of("document", "report", "view", "user", "bob"),
            Relationship.of("document", "report", "view", "user", "charlie"));

        assertThat(results).hasSize(3);
        assertThat(results).containsExactly(true, true, true);
    }

    @Test
    void checkAll_returns_true_when_all_have_permission() {
        boolean allCanView = client.checkAll(
            atLeast(writeRevision), "view",
            Relationship.of("document", "report", "view", "user", "alice"),
            Relationship.of("document", "report", "view", "user", "bob"),
            Relationship.of("document", "report", "view", "user", "charlie"));

        assertThat(allCanView).isTrue();
    }

    @Test
    void checkAll_returns_false_when_not_all_have_permission() {
        boolean allCanEdit = client.checkAll(
            atLeast(writeRevision), "edit",
            Relationship.of("document", "report", "edit", "user", "alice"),
            Relationship.of("document", "report", "edit", "user", "bob"));

        // bob is only a viewer, not an editor
        assertThat(allCanEdit).isFalse();
    }

    @Test
    void checkAny_returns_true_when_at_least_one_has_permission() {
        boolean anyCanEdit = client.checkAny(
            atLeast(writeRevision), "edit",
            Relationship.of("document", "report", "edit", "user", "alice"),
            Relationship.of("document", "report", "edit", "user", "bob"));

        // alice is an editor
        assertThat(anyCanEdit).isTrue();
    }
}
