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
 * Demonstrates bulk permission checks using {@link com.authzed.spicedb.SpiceDBClient#checkPermissions},
 * {@link com.authzed.spicedb.SpiceDBClient#checkAll}, and {@link com.authzed.spicedb.SpiceDBClient#checkAny}.
 */
class BulkOperationsTest extends SpiceDBIntegrationTest {

    private String writeRevision;

    @BeforeEach
    void setUp() {
        client.deleteRelationships(Filter.of("document"));

        // Bulk write relationships
        var txn = new Transaction();
        for (String user : List.of("alice", "bob", "charlie")) {
            txn.touch(Relationship.of("document", "report", "viewer", "user", user));
        }
        txn.touch(Relationship.of("document", "report", "editor", "user", "alice"));
        writeRevision = client.write(txn);
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
