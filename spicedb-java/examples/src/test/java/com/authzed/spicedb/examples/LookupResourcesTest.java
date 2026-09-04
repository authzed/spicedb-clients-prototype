package com.authzed.spicedb.examples;

import static com.authzed.spicedb.Consistency.*;
import static org.assertj.core.api.Assertions.*;

import com.authzed.spicedb.Filter;
import com.authzed.spicedb.LookupResult;
import com.authzed.spicedb.Relationship;
import com.authzed.spicedb.Transaction;
import java.util.List;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

/**
 * Demonstrates finding resources a subject can access using {@link
 * com.authzed.spicedb.SpiceDBClient#lookupResources}.
 */
class LookupResourcesTest extends SpiceDBIntegrationTest {

  @BeforeEach
  void setUp() {
    client.deleteRelationships(Filter.of("document"));

    var txn = new Transaction();
    txn.touch(Relationship.of("document", "firstdoc", "viewer", "user", "alice"));
    txn.touch(Relationship.of("document", "seconddoc", "editor", "user", "alice"));
    txn.touch(Relationship.of("document", "thirddoc", "owner", "user", "bob"));
    client.write(txn);
  }

  @Test
  void alice_can_view_two_documents() {
    List<LookupResult.LookupResource> results;
    try (var stream = client.lookupResources(full(), "document", "view", "user", "alice")) {
      results = stream.toList();
    }

    // Every result carries a permissionship — callers must check it before treating a
    // CONDITIONAL_PERMISSION result as a full grant (see wildcard/caveat-aware examples).
    //
    // Results also aren't guaranteed unique: the same resourceId can be yielded more than
    // once (e.g. via caveated/conditional results), possibly with a different permissionship
    // each time. A caller that needs uniqueness must deduplicate by resourceId itself.
    assertThat(results)
        .allSatisfy(
            r ->
                assertThat(r.permissionship())
                    .isEqualTo(LookupResult.Permissionship.HAS_PERMISSION));
    List<String> resourceIDs =
        results.stream().map(LookupResult.LookupResource::resourceId).toList();
    assertThat(resourceIDs).containsExactlyInAnyOrder("firstdoc", "seconddoc");
  }

  @Test
  void alice_can_edit_only_seconddoc() {
    List<String> resourceIDs;
    try (var stream = client.lookupResources(full(), "document", "edit", "user", "alice")) {
      resourceIDs = stream.map(LookupResult.LookupResource::resourceId).toList();
    }

    assertThat(resourceIDs).containsExactly("seconddoc");
  }

  @Test
  void bob_can_delete_thirddoc() {
    List<String> resourceIDs;
    try (var stream = client.lookupResources(full(), "document", "delete", "user", "bob")) {
      resourceIDs = stream.map(LookupResult.LookupResource::resourceId).toList();
    }

    assertThat(resourceIDs).containsExactly("thirddoc");
  }
}
