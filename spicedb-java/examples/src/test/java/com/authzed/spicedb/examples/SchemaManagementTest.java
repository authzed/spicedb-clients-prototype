package com.authzed.spicedb.examples;

import com.authzed.spicedb.SpiceDBClient;

import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import static org.assertj.core.api.Assertions.*;

/**
 * Demonstrates reading and writing schema using
 * {@link SpiceDBClient#writeSchema} and {@link SpiceDBClient#readSchema}.
 */
class SchemaManagementTest {

    private SpiceDBClient client;

    @BeforeEach
    void setUp() {
        client = SpiceDBClient.createPlaintext("localhost:50051", "somerandomkeyhere");
    }

    @AfterEach
    void tearDown() {
        client.close();
    }

    @Test
    void write_schema_returns_revision() {
        String revision = client.writeSchema("""
            definition user {}

            definition document {
                relation viewer: user
                relation editor: user
                relation owner: user
                permission view = viewer + editor + owner
                permission edit = editor + owner
                permission delete = owner
            }""");

        assertThat(revision).isNotEmpty();
    }

    @Test
    void read_schema_returns_written_definitions() {
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

        SpiceDBClient.SchemaResult result = client.readSchema();

        assertThat(result.revision()).isNotEmpty();
        assertThat(result.schema()).contains("definition user");
        assertThat(result.schema()).contains("definition document");
        assertThat(result.schema()).contains("permission view");
    }

    @Test
    void write_updated_schema_with_new_relation() {
        client.writeSchema("""
            definition user {}

            definition document {
                relation viewer: user
                permission view = viewer
            }""");

        // Update the schema with an additional relation
        String revision = client.writeSchema("""
            definition user {}

            definition document {
                relation viewer: user
                relation editor: user
                permission view = viewer + editor
                permission edit = editor
            }""");

        assertThat(revision).isNotEmpty();

        SpiceDBClient.SchemaResult result = client.readSchema();
        assertThat(result.schema()).contains("relation editor");
        assertThat(result.schema()).contains("permission edit");
    }
}
