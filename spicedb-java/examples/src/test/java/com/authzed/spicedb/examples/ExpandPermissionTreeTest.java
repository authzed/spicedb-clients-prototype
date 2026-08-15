package com.authzed.spicedb.examples;

import static com.authzed.spicedb.Consistency.*;
import static org.assertj.core.api.Assertions.*;

import com.authzed.spicedb.Filter;
import com.authzed.spicedb.PermissionTree;
import com.authzed.spicedb.PermissionTree.LeafNode;
import com.authzed.spicedb.PermissionTree.Operation;
import com.authzed.spicedb.PermissionTree.SubjectRef;
import com.authzed.spicedb.Relationship;
import com.authzed.spicedb.SpiceDBClient.ExpandResult;
import com.authzed.spicedb.Transaction;
import java.util.HashSet;
import java.util.Set;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

/**
 * Demonstrates expanding a permission into its full tree of subjects using {@link
 * com.authzed.spicedb.SpiceDBClient#expandPermissionTree}, and walking the native {@link
 * PermissionTree} result (expandedObject, expandedRelation, and exactly one of
 * intermediate/leaf) down to its leaf subjects.
 */
class ExpandPermissionTreeTest extends SpiceDBIntegrationTest {

  private String writeRevision;

  @BeforeEach
  void setUp() {
    client.deleteRelationships(Filter.of("document"));

    var txn = new Transaction();
    txn.touch(Relationship.of("document", "report", "viewer", "user", "alice"));
    txn.touch(Relationship.of("document", "report", "editor", "user", "bob"));
    txn.touch(Relationship.of("document", "report", "owner", "user", "charlie"));
    writeRevision = client.write(txn);
  }

  @Test
  void expand_view_permission_returns_union_of_all_relations() {
    ExpandResult result =
        client.expandPermissionTree(atLeast(writeRevision), "document", "report", "view");

    assertThat(result.revision()).isNotEmpty();

    PermissionTree tree = result.tree();
    assertThat(tree.expandedObject().objectType()).isEqualTo("document");
    assertThat(tree.expandedObject().objectID()).isEqualTo("report");
    assertThat(tree.expandedRelation()).isEqualTo("view");

    // "view = viewer + editor + owner" is a union, so the root of the tree is an intermediate
    // node combining the three relations' subtrees.
    assertThat(tree.intermediate()).isNotNull();
    assertThat(tree.leaf()).isNull();
    assertThat(tree.intermediate().operation()).isEqualTo(Operation.UNION);

    Set<String> subjectIDs = collectLeafSubjectIDs(tree);
    assertThat(subjectIDs).containsExactlyInAnyOrder("alice", "bob", "charlie");
  }

  @Test
  void expand_delete_permission_resolves_to_owner_leaf() {
    // "delete = owner" is a single relation with no set operation, which SpiceDB may expand
    // straight to a leaf node rather than wrapping it in a one-child intermediate node. Walking
    // the tree generically (rather than asserting on its exact shape) handles either case.
    ExpandResult result =
        client.expandPermissionTree(atLeast(writeRevision), "document", "report", "delete");

    Set<String> subjectIDs = collectLeafSubjectIDs(result.tree());
    assertThat(subjectIDs).containsExactly("charlie");
  }

  /**
   * Recursively walks a {@link PermissionTree}, collecting subject IDs from every leaf node.
   * Exactly one of {@link PermissionTree#intermediate()} or {@link PermissionTree#leaf()} is
   * non-null on any given node.
   */
  private static Set<String> collectLeafSubjectIDs(PermissionTree tree) {
    Set<String> subjectIDs = new HashSet<>();
    walk(tree, subjectIDs);
    return subjectIDs;
  }

  private static void walk(PermissionTree tree, Set<String> subjectIDs) {
    LeafNode leaf = tree.leaf();
    if (leaf != null) {
      for (SubjectRef subject : leaf.subjects()) {
        subjectIDs.add(subject.subjectID());
      }
      return;
    }

    for (PermissionTree child : tree.intermediate().children()) {
      walk(child, subjectIDs);
    }
  }
}
