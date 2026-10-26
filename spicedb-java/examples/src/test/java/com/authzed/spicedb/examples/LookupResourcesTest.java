package com.authzed.spicedb.examples;

import static com.authzed.spicedb.Consistency.*;
import static org.assertj.core.api.Assertions.*;

import com.authzed.spicedb.*;
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
    client.deleteAllRelationshipsNonAtomic(Filter.of("document"));

    var txn = new Transaction();
    txn.touch(
        Relationship.of(
            new ObjectRef("document", "firstdoc"),
            "viewer",
            SubjectRef.direct(new ObjectRef("user", "alice"))));
    txn.touch(
        Relationship.of(
            new ObjectRef("document", "seconddoc"),
            "editor",
            SubjectRef.direct(new ObjectRef("user", "alice"))));
    txn.touch(
        Relationship.of(
            new ObjectRef("document", "thirddoc"),
            "owner",
            SubjectRef.direct(new ObjectRef("user", "bob"))));
    client.write(txn);
  }

  @Test
  void alice_can_view_two_documents() {
    List<LookupResult.LookupResource> results;
    try (var stream =
        client.lookupResources(
            full(),
            "document",
            "view",
            SubjectRef.direct(new ObjectRef("user", "alice")),
            LookupOptions.builder()
                .permissionshipFilter(LookupPermissionshipFilter.HAS_PERMISSION_ONLY)
                .build())) {
      results = stream.toList();
    }

    // The optional client-side filter admits only unconditional HAS_PERMISSION results.
    // Without it, filter explicitly before mapping IDs (as shown below).
    //
    // Results also aren't guaranteed unique: the same resourceId can be yielded more than
    // once (e.g. via caveated/conditional results), possibly with a different permissionship
    // each time. A caller that needs uniqueness must deduplicate by resourceId itself.
    assertThat(results)
        .allSatisfy(r -> assertThat(r.permissionship()).isEqualTo(Permissionship.HAS_PERMISSION));
    List<String> resourceIDs =
        results.stream().map(LookupResult.LookupResource::resourceId).toList();
    assertThat(resourceIDs).containsExactlyInAnyOrder("firstdoc", "seconddoc");
  }

  @Test
  void alice_can_edit_only_seconddoc() {
    List<String> resourceIDs;
    try (var stream =
        client.lookupResources(
            full(), "document", "edit", SubjectRef.direct(new ObjectRef("user", "alice")))) {
      resourceIDs =
          stream
              .filter(result -> result.permissionship() == Permissionship.HAS_PERMISSION)
              .map(LookupResult.LookupResource::resourceId)
              .toList();
    }

    assertThat(resourceIDs).containsExactly("seconddoc");
  }

  @Test
  void bob_can_delete_thirddoc() {
    List<String> resourceIDs;
    try (var stream =
        client.lookupResources(
            full(), "document", "delete", SubjectRef.direct(new ObjectRef("user", "bob")))) {
      resourceIDs =
          stream
              .filter(result -> result.permissionship() == Permissionship.HAS_PERMISSION)
              .map(LookupResult.LookupResource::resourceId)
              .toList();
    }

    assertThat(resourceIDs).containsExactly("thirddoc");
  }
}
