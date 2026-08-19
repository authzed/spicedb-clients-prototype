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
	if len(result.Definitions) == 0 {
		log.Fatalf("expected at least one definition")
	}
	for _, def := range result.Definitions {
		fmt.Printf("  definition %s: %d relations, %d permissions\n",
			def.Name, len(def.Relations), len(def.Permissions))
	}

	// Find computable permissions for a relation
	perms, revision, err := c.ComputablePermissions(ctx, consistency.Full(), "document", "viewer")
	if err != nil {
		log.Fatalf("computable permissions failed: %v", err)
	}
	fmt.Printf("\ncomputable permissions for document#viewer (revision: %s):\n", revision)
	if len(perms) == 0 {
		log.Fatalf("expected at least one computable permission")
	}
	for _, p := range perms {
		fmt.Printf("  %s#%s (is_permission=%v)\n", p.DefinitionName, p.RelationName, p.IsPermission)
	}

	// Find dependent relations for a permission
	deps, revision, err := c.DependentRelations(ctx, consistency.Full(), "document", "view")
	if err != nil {
		log.Fatalf("dependent relations failed: %v", err)
	}
	fmt.Printf("\ndependent relations for document#view (revision: %s):\n", revision)
	if len(deps) == 0 {
		log.Fatalf("expected at least one dependent relation")
	}
	for _, d := range deps {
		fmt.Printf("  %s#%s\n", d.DefinitionName, d.RelationName)
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
	if len(diffs) == 0 {
		log.Fatalf("expected at least one diff")
	}
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
}
