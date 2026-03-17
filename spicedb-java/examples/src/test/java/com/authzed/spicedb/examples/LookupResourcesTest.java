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
 * Demonstrates finding resources a subject can access using
 * {@link SpiceDBClient#lookupResources}.
 */
class LookupResourcesTest {

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
        txn.touch(Relationship.of("document", "seconddoc", "editor", "user", "alice"));
        txn.touch(Relationship.of("document", "thirddoc", "owner", "user", "bob"));
        client.write(txn);
    }

    @AfterEach
    void tearDown() {
        client.close();
    }

    @Test
    void alice_can_view_two_documents() {
        List<String> resourceIDs;
        try (var stream = client.lookupResources(full(), "document", "view", "user", "alice")) {
            resourceIDs = stream.toList();
        }

        assertThat(resourceIDs).containsExactlyInAnyOrder("firstdoc", "seconddoc");
    }

    @Test
    void alice_can_edit_only_seconddoc() {
        List<String> resourceIDs;
        try (var stream = client.lookupResources(full(), "document", "edit", "user", "alice")) {
            resourceIDs = stream.toList();
        }

        assertThat(resourceIDs).containsExactly("seconddoc");
    }

    @Test
    void bob_can_delete_thirddoc() {
        List<String> resourceIDs;
        try (var stream = client.lookupResources(full(), "document", "delete", "user", "bob")) {
            resourceIDs = stream.toList();
        }

        assertThat(resourceIDs).containsExactly("thirddoc");
    }
}
