package com.authzed.spicedb.examples;

import static com.authzed.spicedb.Consistency.*;
import static org.assertj.core.api.Assertions.*;

import com.authzed.spicedb.Filter;
import com.authzed.spicedb.Relationship;
import com.authzed.spicedb.Transaction;
import com.authzed.spicedb.errors.FailedPreconditionException;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

/**
 * Demonstrates writing relationships with the {@link Transaction} builder, including touch, create,
 * delete, and preconditions.
 */
class WriteRelationshipsTest extends SpiceDBIntegrationTest {

  @BeforeEach
  void setUp() {
    client.deleteRelationships(Filter.of("document"));
  }

  @Test
  void touch_creates_relationships_and_returns_revision() {
    var txn = new Transaction();
    txn.touch(Relationship.of("document", "firstdoc", "viewer", "user", "alice"));
    txn.touch(Relationship.of("document", "firstdoc", "editor", "user", "bob"));

    String revision = client.write(txn);

    assertThat(revision).isNotEmpty();
  }

  @Test
  void precondition_mustNotMatch_succeeds_when_no_match() {
    var txn = new Transaction();
    txn.touch(Relationship.of("document", "firstdoc", "viewer", "user", "alice"));
    txn.mustNotMatch(
        Filter.of("document")
            .withResourceID("firstdoc")
            .withRelation("owner")
            .withSubjectType("user")
            .withSubjectID("mallory"));

    String revision = client.write(txn);

    assertThat(revision).isNotEmpty();
  }

  /**
   * A failed precondition arrives with SpiceDB's structured explanation attached, not just a
   * message: the typed exception carries the {@code authzed.api.v1.ErrorReason} name and the
   * metadata naming which precondition did not hold, so recovery can be written against data rather
   * than against a parsed string.
   */
  @Test
  void precondition_failure_carries_the_reason_and_its_metadata() {
    var txn = new Transaction();
    txn.touch(Relationship.of("document", "seconddoc", "viewer", "user", "alice"));
    txn.mustMatch(
        Filter.of("document")
            .withResourceID("firstdoc")
            .withRelation("owner")
            .withSubjectType("user")
            .withSubjectID("nobody"));

    Throwable thrown = catchThrowable(() -> client.write(txn));

    assertThat(thrown).isInstanceOf(FailedPreconditionException.class);
    FailedPreconditionException failure = (FailedPreconditionException) thrown;
    assertThat(failure.getReason()).isEqualTo("ERROR_REASON_WRITE_OR_DELETE_PRECONDITION_FAILURE");
    assertThat(failure.getReasonDomain()).isEqualTo("authzed.com");
    assertThat(failure.getReasonMetadata()).containsEntry("precondition_resource_id", "firstdoc");
  }

  @Test
  void delete_removes_relationship() {
    // First create
    var create = new Transaction();
    create.touch(Relationship.of("document", "deldoc", "viewer", "user", "charlie"));
    client.write(create);

    // Then delete
    var del = new Transaction();
    del.delete(Relationship.of("document", "deldoc", "viewer", "user", "charlie"));
    String revision = client.write(del);

    assertThat(revision).isNotEmpty();

    // Verify it's gone
    long count =
        client
            .readRelationships(
                full(), Filter.of("document").withResourceID("deldoc").withRelation("viewer"))
            .count();
    assertThat(count).isZero();
  }
}
