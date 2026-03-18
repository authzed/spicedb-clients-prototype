package com.authzed.spicedb.examples;

import com.authzed.spicedb.SpiceDBClient.ComputablePermissionsResult;
import com.authzed.spicedb.SpiceDBClient.DependentRelationsResult;
import com.authzed.spicedb.SpiceDBClient.DiffSchemaResult;
import com.authzed.spicedb.SpiceDBClient.ReflectSchemaResult;
import com.authzed.spicedb.SpiceDBClient.SchemaDefinition;

import org.junit.jupiter.api.Test;

import static com.authzed.spicedb.Consistency.*;
import static org.assertj.core.api.Assertions.*;

/**
 * Demonstrates schema reflection APIs: inspecting definitions, computing
 * permissions, finding dependent relations, and diffing schemas.
 */
class SchemaReflectionTest extends SpiceDBIntegrationTest {

    @Test
    void reflect_schema_returns_definitions() {
        ReflectSchemaResult result = client.reflectSchema(full());

        assertThat(result.revision()).isNotEmpty();
        assertThat(result.definitions()).hasSizeGreaterThanOrEqualTo(2);

        SchemaDefinition docDef = result.definitions().stream()
            .filter(d -> d.name().equals("document"))
            .findFirst()
            .orElseThrow();

        assertThat(docDef.relations()).extracting("name")
            .containsExactlyInAnyOrder("viewer", "editor", "owner");
        assertThat(docDef.permissions()).extracting("name")
            .containsExactlyInAnyOrder("view", "edit", "delete");
    }

    @Test
    void computable_permissions_for_viewer_relation() {
        ComputablePermissionsResult result =
            client.computablePermissions(full(), "document", "viewer");

        assertThat(result.revision()).isNotEmpty();
        assertThat(result.permissions()).isNotEmpty();
        assertThat(result.permissions())
            .extracting("relationName")
            .contains("view");
    }

    @Test
    void dependent_relations_for_view_permission() {
        DependentRelationsResult result =
            client.dependentRelations(full(), "document", "view");

        assertThat(result.revision()).isNotEmpty();
        assertThat(result.relations()).isNotEmpty();
        assertThat(result.relations())
            .extracting("relationName")
            .contains("viewer", "editor", "owner");
    }

    @Test
    void diff_schema_detects_added_relation_and_permission() {
        String newSchema = """
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

        DiffSchemaResult result = client.diffSchema(full(), newSchema);

        assertThat(result.revision()).isNotEmpty();
        assertThat(result.diffs()).isNotEmpty();
    }
}
