// Example schema_reflection demonstrates using schema reflection APIs to
// inspect definitions, compute permissions, find dependent relations, and
// diff schemas.
package main

import (
	"cmp"
	"context"
	"fmt"
	"log"
	"os"
	"slices"

	"github.com/authzed/spicedb-clients/spicedb-go/client"
	"github.com/authzed/spicedb-clients/spicedb-go/consistency"
)

func main() {
	// Endpoint and token come from the environment so the example runs against
	// whichever SpiceDB the caller started; the defaults match
	// docker-compose.test.yml.
	c, err := client.NewPlaintext(
		cmp.Or(os.Getenv("SPICEDB_ENDPOINT"), "localhost:50051"),
		cmp.Or(os.Getenv("SPICEDB_TOKEN"), "somerandomkeyhere"),
	)
	if err != nil {
		log.Fatalf("failed to create client: %v", err)
	}
	defer func() { _ = c.Close() }()

	ctx := context.Background()

	// Setup: write schema
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

	// Reflect the current schema
	result, err := c.ReflectSchema(ctx, consistency.Full())
	if err != nil {
		log.Fatalf("reflect schema failed: %v", err)
	}
	fmt.Printf("schema has %d definitions and %d caveats (revision: %s)\n",
		len(result.Definitions), len(result.Caveats), result.Revision)
	for _, def := range result.Definitions {
		fmt.Printf("  definition %s: %d relations, %d permissions\n",
			def.Name, len(def.Relations), len(def.Permissions))
	}

	// Assert the shape of what came back, not merely that something did. A
	// non-empty check passes on a reflection that returned one definition with
	// no relations and no permissions -- which is what dropping the nested
	// conversion loops would produce.
	document := findDefinition(result.Definitions, "document")
	if document == nil {
		log.Fatalf("expected a `document` definition in the reflected schema, got %v",
			definitionNames(result.Definitions))
	}
	if findDefinition(result.Definitions, "user") == nil {
		log.Fatalf("expected a `user` definition in the reflected schema, got %v",
			definitionNames(result.Definitions))
	}
	if got := relationNames(document.Relations); !slices.Equal(got, []string{"editor", "owner", "viewer"}) {
		log.Fatalf("expected document's relations to be [editor owner viewer], got %v", got)
	}
	if got := permissionNames(document.Permissions); !slices.Equal(got, []string{"delete", "edit", "view"}) {
		log.Fatalf("expected document's permissions to be [delete edit view], got %v", got)
	}
	// Relations and permissions are distinct lists: a reflection that conflated
	// them would put `view` in Relations, which the two checks above catch
	// together but neither catches alone.
	if result.Revision == "" {
		log.Fatalf("expected a non-empty revision from ReflectSchema")
	}

	// Find computable permissions for a relation
	perms, revision, err := c.ComputablePermissions(ctx, consistency.Full(), "document", "viewer")
	if err != nil {
		log.Fatalf("computable permissions failed: %v", err)
	}
	fmt.Printf("\ncomputable permissions for document#viewer (revision: %s):\n", revision)
	for _, p := range perms {
		fmt.Printf("  %s#%s (is_permission=%v)\n", p.DefinitionName, p.RelationName, p.IsPermission)
	}
	// `viewer` appears in `view` and in nothing else in this schema, so the
	// answer is exactly one reference, and it names the permission rather than
	// the relation it was asked about.
	if got := referenceNames(perms); !slices.Equal(got, []string{"document#view"}) {
		log.Fatalf("expected computable permissions for document#viewer to be [document#view], got %v", got)
	}
	if !perms[0].IsPermission {
		log.Fatalf("expected document#view to be reported as a permission, not a relation")
	}
	if revision == "" {
		log.Fatalf("expected a non-empty revision from ComputablePermissions")
	}

	// Find dependent relations for a permission
	deps, revision, err := c.DependentRelations(ctx, consistency.Full(), "document", "view")
	if err != nil {
		log.Fatalf("dependent relations failed: %v", err)
	}
	fmt.Printf("\ndependent relations for document#view (revision: %s):\n", revision)
	for _, d := range deps {
		fmt.Printf("  %s#%s\n", d.DefinitionName, d.RelationName)
	}
	// `view = viewer + editor + owner`, so all three relations are dependencies
	// and nothing else is.
	if got := referenceNames(deps); !slices.Equal(got, []string{"document#editor", "document#owner", "document#viewer"}) {
		log.Fatalf("expected dependent relations of document#view to be "+
			"[document#editor document#owner document#viewer], got %v", got)
	}
	if revision == "" {
		log.Fatalf("expected a non-empty revision from DependentRelations")
	}

	// Diff against a modified schema (adds a new "admin" relation and "manage" permission)
	newSchema := `definition user {}

definition document {
	relation viewer: user
	relation editor: user
	relation owner: user
	relation admin: user
	permission view = viewer + editor + owner + admin
	permission edit = editor + owner + admin
	permission delete = owner + admin
	permission manage = admin
}`
	diffs, revision, err := c.DiffSchema(ctx, consistency.Full(), newSchema)
	if err != nil {
		log.Fatalf("diff schema failed: %v", err)
	}
	fmt.Printf("\nschema diffs (revision: %s):\n", revision)
	for _, d := range diffs {
		fmt.Printf("  %s", d.Kind)
		if d.DefinitionName != "" {
			fmt.Printf(" on %s", d.DefinitionName)
		}
		if d.RelationName != "" {
			fmt.Printf("#%s", d.RelationName)
		}
		if d.PermissionName != "" {
			fmt.Printf("#%s", d.PermissionName)
		}
		fmt.Println()
	}

	// newSchema adds one relation and one permission and changes the
	// expression of the other three, so the diff is a known set. Asserting
	// only that it is non-empty would pass on a diff that reported every kind
	// as "unknown" -- the exact outcome of the mapping in schemaDiffFromProto
	// falling through to its default branch.
	if !hasDiff(diffs, client.SchemaDiff{Kind: "relation_added", DefinitionName: "document", RelationName: "admin"}) {
		log.Fatalf("expected a relation_added diff for document#admin, got %v", diffs)
	}
	if !hasDiff(diffs, client.SchemaDiff{Kind: "permission_added", DefinitionName: "document", PermissionName: "manage"}) {
		log.Fatalf("expected a permission_added diff for document#manage, got %v", diffs)
	}
	if !hasDiff(diffs, client.SchemaDiff{Kind: "permission_expr_changed", DefinitionName: "document", PermissionName: "view"}) {
		log.Fatalf("expected a permission_expr_changed diff for document#view, got %v", diffs)
	}
	for _, d := range diffs {
		if d.Kind == "unknown" {
			log.Fatalf("a diff came back as \"unknown\": the proto-to-native diff mapping "+
				"does not cover what the server sent (all diffs: %v)", diffs)
		}
	}
	if revision == "" {
		log.Fatalf("expected a non-empty revision from DiffSchema")
	}
}

// findDefinition returns the named definition, or nil when it is absent.
func findDefinition(defs []client.SchemaDefinition, name string) *client.SchemaDefinition {
	for i := range defs {
		if defs[i].Name == name {
			return &defs[i]
		}
	}
	return nil
}

func definitionNames(defs []client.SchemaDefinition) []string {
	names := make([]string, 0, len(defs))
	for _, d := range defs {
		names = append(names, d.Name)
	}
	slices.Sort(names)
	return names
}

func relationNames(relations []client.SchemaRelation) []string {
	names := make([]string, 0, len(relations))
	for _, r := range relations {
		names = append(names, r.Name)
	}
	slices.Sort(names)
	return names
}

func permissionNames(permissions []client.SchemaPermission) []string {
	names := make([]string, 0, len(permissions))
	for _, p := range permissions {
		names = append(names, p.Name)
	}
	slices.Sort(names)
	return names
}

// referenceNames renders relation references as "definition#relation", sorted,
// so a whole answer can be compared in one assertion.
func referenceNames(refs []client.RelationReference) []string {
	names := make([]string, 0, len(refs))
	for _, r := range refs {
		names = append(names, r.DefinitionName+"#"+r.RelationName)
	}
	slices.Sort(names)
	return names
}

func hasDiff(diffs []client.SchemaDiff, want client.SchemaDiff) bool {
	return slices.Contains(diffs, want)
}
