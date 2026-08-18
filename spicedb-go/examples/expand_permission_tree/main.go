// Example expand_permission_tree demonstrates expanding a permission into its
// full tree of subjects using ExpandPermissionTree, and walking the native
// PermissionTree result (ExpandedObject, Intermediate/Leaf nodes, subjects).
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/authzed/spicedb-clients/spicedb-go/client"
	"github.com/authzed/spicedb-clients/spicedb-go/consistency"
	"github.com/authzed/spicedb-clients/spicedb-go/rel"
)

func main() {
	c, err := client.NewPlaintext("localhost:50051", "somerandomkeyhere")
	if err != nil {
		log.Fatalf("failed to create client: %v", err)
	}

	ctx := context.Background()

	// Setup: write schema and test data
	_, err = c.WriteSchema(ctx, `definition user {}

definition document {
	relation viewer: user
	relation editor: user
	relation owner: user
	permission view = viewer + editor + owner
	permission edit = editor + owner
	permission delete = owner
}`)
	if err != nil {
		log.Fatalf("write schema failed: %v", err)
	}

	var txn rel.Txn
	if err := txn.Touch(rel.MustFromTriple("document", "firstdoc", "viewer", "user", "alice", "")); err != nil {
		log.Fatalf("failed to add relationship to transaction: %v", err)
	}
	if err := txn.Touch(rel.MustFromTriple("document", "firstdoc", "editor", "user", "bob", "")); err != nil {
		log.Fatalf("failed to add relationship to transaction: %v", err)
	}
	_, err = c.Write(ctx, txn)
	if err != nil {
		log.Fatalf("write relationships failed: %v", err)
	}

	// Expand the "view" permission into its full tree of subjects.
	result, err := c.ExpandPermissionTree(ctx, consistency.Full(), "document", "firstdoc", "view")
	if err != nil {
		log.Fatalf("expand permission tree failed: %v", err)
	}

	fmt.Printf("expanded %s:%s#%s\n", result.Tree.ExpandedObject.ObjectType, result.Tree.ExpandedObject.ObjectID, result.Tree.ExpandedRelation)
	if result.Tree.ExpandedObject.ObjectType != "document" || result.Tree.ExpandedObject.ObjectID != "firstdoc" {
		log.Fatalf("unexpected expanded object: %+v", result.Tree.ExpandedObject)
	}

	// "view = viewer + editor + owner" is a union, so the root of the tree is
	// an intermediate node combining the three relations' subtrees.
	if result.Tree.Intermediate == nil {
		log.Fatalf("expected root node to be an intermediate (union) node")
	}
	fmt.Printf("root is an intermediate node: operation=%v children=%d\n", result.Tree.Intermediate.Operation, len(result.Tree.Intermediate.Children))
	if result.Tree.Intermediate.Operation != client.TreeOperationUnion {
		log.Fatalf("expected union operation at root, got %v", result.Tree.Intermediate.Operation)
	}

	// Walk the tree, collecting subjects found at leaf nodes.
	subjects := map[string]bool{}
	collectLeafSubjects(result.Tree, subjects)

	for id := range subjects {
		fmt.Printf("subject with view access: user:%s\n", id)
	}

	if !subjects["alice"] {
		log.Fatalf("expected alice (viewer) to appear in the expanded tree")
	}
	if !subjects["bob"] {
		log.Fatalf("expected bob (editor) to appear in the expanded tree")
	}

	// Clean up so later examples that write a narrower schema aren't blocked
	// by leftover relationships (examples run in sequence against one shared
	// SpiceDB instance).
	if _, err := c.DeleteRelationships(ctx, rel.NewFilter("document")); err != nil {
		log.Fatalf("cleanup failed: %v", err)
	}
}

// collectLeafSubjects recursively walks a PermissionTree, gathering subject
// IDs from every leaf node. Exactly one of Intermediate or Leaf is non-nil on
// any given node.
func collectLeafSubjects(t client.PermissionTree, subjects map[string]bool) {
	if leaf := t.Leaf; leaf != nil {
		for _, s := range leaf.Subjects {
			subjects[s.SubjectID] = true
		}
	}
	if intermediate := t.Intermediate; intermediate != nil {
		for _, child := range intermediate.Children {
			collectLeafSubjects(child, subjects)
		}
	}
}
