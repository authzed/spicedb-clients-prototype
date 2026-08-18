package com.authzed.spicedb.examples;

import static com.authzed.spicedb.Consistency.*;
import static org.assertj.core.api.Assertions.*;

import com.authzed.spicedb.CheckResult;
import com.authzed.spicedb.Filter;
import com.authzed.spicedb.Relationship;
import com.authzed.spicedb.Transaction;
import java.util.List;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

/**
 * Demonstrates bulk permission checks using {@link
 * com.authzed.spicedb.SpiceDBClient#checkPermissions}, {@link
 * com.authzed.spicedb.SpiceDBClient#checkAll}, and {@link
 * com.authzed.spicedb.SpiceDBClient#checkAny}, as well as bulk relationship import/export using
 * {@link com.authzed.spicedb.SpiceDBClient#importRelationships} and {@link
 * com.authzed.spicedb.SpiceDBClient#exportRelationships}.
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
    List<CheckResult> results =
        client.checkPermissions(
            atLeast(writeRevision),
            "view",
            Relationship.of("document", "report", "view", "user", "alice"),
            Relationship.of("document", "report", "view", "user", "bob"),
            Relationship.of("document", "report", "view", "user", "charlie"));

    assertThat(results).hasSize(3);
    assertThat(results).allSatisfy(r -> assertThat(r.hasPermission()).isTrue());
    // Every result carries checked_at, threadable into Consistency.atLeast for read-your-writes.
    assertThat(results).allSatisfy(r -> assertThat(r.checkedAt()).isNotEmpty());
  }

  @Test
  void checkAll_returns_true_when_all_have_permission() {
    boolean allCanView =
        client.checkAll(
            atLeast(writeRevision),
            "view",
            Relationship.of("document", "report", "view", "user", "alice"),
            Relationship.of("document", "report", "view", "user", "bob"),
            Relationship.of("document", "report", "view", "user", "charlie"));

    assertThat(allCanView).isTrue();
  }

  @Test
  void checkAll_returns_false_when_not_all_have_permission() {
    boolean allCanEdit =
        client.checkAll(
            atLeast(writeRevision),
            "edit",
            Relationship.of("document", "report", "edit", "user", "alice"),
            Relationship.of("document", "report", "edit", "user", "bob"));

    // bob is only a viewer, not an editor
    assertThat(allCanEdit).isFalse();
  }

  @Test
  void checkAny_returns_true_when_at_least_one_has_permission() {
    boolean anyCanEdit =
        client.checkAny(
            atLeast(writeRevision),
            "edit",
            Relationship.of("document", "report", "edit", "user", "alice"),
            Relationship.of("document", "report", "edit", "user", "bob"));

    // alice is an editor
    assertThat(anyCanEdit).isTrue();
  }

  @Test
  void bulk_import_relationships_reports_number_loaded() {
    // importRelationships streams relationships to the server in batches (1,000-item chunks)
    // and returns the number loaded -- useful for large, one-shot data loads, unlike write(),
    // which is transactional and limited in size.
    List<Relationship> imported =
        List.of(
            Relationship.of("document", "bulkdoc1", "viewer", "user", "dave"),
            Relationship.of("document", "bulkdoc2", "viewer", "user", "erin"),
            Relationship.of("document", "bulkdoc3", "viewer", "user", "frank"));

    long numLoaded = client.importRelationships(imported);

    assertThat(numLoaded).isEqualTo(imported.size());
  }

  @Test
  void bulk_export_relationships_streams_matching_relationships() {
    List<Relationship> imported =
        List.of(
            Relationship.of("document", "bulkdoc1", "viewer", "user", "dave"),
            Relationship.of("document", "bulkdoc2", "viewer", "user", "erin"),
            Relationship.of("document", "bulkdoc3", "viewer", "user", "frank"));
    client.importRelationships(imported);

    // exportRelationships streams relationships back out, paginating transparently under the
    // hood (512-item pages by default). The returned stream should be closed when done.
    Filter exportFilter = Filter.of("document").withResourceIDPrefix("bulkdoc");
    List<Relationship> exported;
    try (var stream = client.exportRelationships(full(), exportFilter)) {
      exported = stream.toList();
    }

    assertThat(exported).hasSize(imported.size());
    assertThat(exported)
        .extracting(Relationship::subjectID)
        .containsExactlyInAnyOrder("dave", "erin", "frank");
  }
}
