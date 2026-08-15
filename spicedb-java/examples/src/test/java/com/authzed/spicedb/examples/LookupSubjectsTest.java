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
 * Demonstrates finding subjects with access to a resource using {@link
 * com.authzed.spicedb.SpiceDBClient#lookupSubjects}.
 */
class LookupSubjectsTest extends SpiceDBIntegrationTest {

  @BeforeEach
  void setUp() {
    client.deleteRelationships(Filter.of("document"));

    var txn = new Transaction();
    txn.touch(Relationship.of("document", "firstdoc", "viewer", "user", "alice"));
    txn.touch(Relationship.of("document", "firstdoc", "editor", "user", "bob"));
    txn.touch(Relationship.of("document", "firstdoc", "owner", "user", "charlie"));
    client.write(txn);
  }

  @Test
  void all_three_users_can_view() {
    List<LookupResult.LookupSubject> results;
    try (var stream = client.lookupSubjects(full(), "document", "firstdoc", "view", "user")) {
      results = stream.toList();
    }

    // None of these are wildcard matches, so excludedSubjects is always empty here — see the
    // lookupSubjects entry in DESIGN.md for the wildcard case callers MUST handle: a "*" subject's
    // excludedSubjects lists subjects NOT actually granted, despite the wildcard match.
    assertThat(results).allSatisfy(r -> assertThat(r.excludedSubjects()).isEmpty());
    List<String> subjectIDs = results.stream().map(r -> r.subject().subjectId()).toList();
    assertThat(subjectIDs).containsExactlyInAnyOrder("alice", "bob", "charlie");
  }

  @Test
  void only_bob_and_charlie_can_edit() {
    List<String> subjectIDs;
    try (var stream = client.lookupSubjects(full(), "document", "firstdoc", "edit", "user")) {
      subjectIDs = stream.map(r -> r.subject().subjectId()).toList();
    }

    assertThat(subjectIDs).containsExactlyInAnyOrder("bob", "charlie");
  }

  @Test
  void only_charlie_can_delete() {
    List<String> subjectIDs;
    try (var stream = client.lookupSubjects(full(), "document", "firstdoc", "delete", "user")) {
      subjectIDs = stream.map(r -> r.subject().subjectId()).toList();
    }

    assertThat(subjectIDs).containsExactly("charlie");
  }
}
