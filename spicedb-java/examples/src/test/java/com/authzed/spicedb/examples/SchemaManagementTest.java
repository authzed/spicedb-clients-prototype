package com.authzed.spicedb.examples;

import com.authzed.spicedb.SpiceDBClient;

import org.junit.jupiter.api.Test;

import static org.assertj.core.api.Assertions.*;

/**
 * Demonstrates reading and writing schema using
 * {@link SpiceDBClient#writeSchema} and {@link SpiceDBClient#readSchema}.
 */
class SchemaManagementTest extends SpiceDBIntegrationTest {

    @Test
    void write_schema_returns_revision() {
        String revision = client.writeSchema(SCHEMA);

        assertThat(revision).isNotEmpty();
    }

    @Test
    void read_schema_returns_written_definitions() {
        SpiceDBClient.SchemaResult result = client.readSchema();

        assertThat(result.revision()).isNotEmpty();
        assertThat(result.schema()).contains("definition user");
        assertThat(result.schema()).contains("definition document");
        assertThat(result.schema()).contains("permission view");
    }

    @Test
    void write_updated_schema_with_new_relation() {
        // Write a schema that adds an admin relation to document
        String updatedSchema = """
            definition user {}

            definition document {
                relation viewer: user
                relation editor: user
                relation owner: user
                relation admin: user
                permission view = viewer + editor + owner + admin
                permission edit = editor + owner + admin
                permission delete = owner + admin
                permission manage = admin
            }""";

        String revision = client.writeSchema(updatedSchema);

        assertThat(revision).isNotEmpty();

        SpiceDBClient.SchemaResult result = client.readSchema();
        assertThat(result.schema()).contains("relation admin: user");
        assertThat(result.schema()).contains("permission manage");

        // Restore the standard schema for subsequent tests
        client.writeSchema(SCHEMA);
    }
}
