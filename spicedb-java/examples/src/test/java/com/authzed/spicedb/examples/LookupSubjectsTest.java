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
 * Demonstrates finding subjects with access to a resource using
 * {@link SpiceDBClient#lookupSubjects}.
 */
class LookupSubjectsTest {

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
        txn.touch(Relationship.of("document", "firstdoc", "owner", "user", "charlie"));
        client.write(txn);
    }

    @AfterEach
    void tearDown() {
        client.close();
    }

    @Test
    void all_three_users_can_view() {
        List<String> subjectIDs;
        try (var stream = client.lookupSubjects(full(), "document", "firstdoc", "view", "user")) {
            subjectIDs = stream.toList();
        }

        assertThat(subjectIDs).containsExactlyInAnyOrder("alice", "bob", "charlie");
    }

    @Test
    void only_bob_and_charlie_can_edit() {
        List<String> subjectIDs;
        try (var stream = client.lookupSubjects(full(), "document", "firstdoc", "edit", "user")) {
            subjectIDs = stream.toList();
        }

        assertThat(subjectIDs).containsExactlyInAnyOrder("bob", "charlie");
    }

    @Test
    void only_charlie_can_delete() {
        List<String> subjectIDs;
        try (var stream = client.lookupSubjects(full(), "document", "firstdoc", "delete", "user")) {
            subjectIDs = stream.toList();
        }

        assertThat(subjectIDs).containsExactly("charlie");
    }
}
