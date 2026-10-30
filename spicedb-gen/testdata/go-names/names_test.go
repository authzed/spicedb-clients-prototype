// Package apinames is generated with --go.names-only. This module's go.mod
// requires nothing, so it builds only while the generated file imports the
// standard library alone: a shared API package can use the schema's names
// without depending on the SpiceDB client.
package apinames

import (
	"slices"
	"strings"
	"testing"
)

func TestNamesOnly(t *testing.T) {
	if Definitions.Document != "document" || DocumentRelations.Viewer != "viewer" || DocumentPermissions.View != "view" {
		t.Fatal("name constants do not match the schema")
	}
	if !slices.Equal(DocumentPermissionNames(), []string{"view", "edit", "delete"}) {
		t.Fatalf("DocumentPermissionNames() = %v", DocumentPermissionNames())
	}
	if !slices.Contains(DefinitionNames(), Definitions.Organization) {
		t.Fatalf("DefinitionNames() = %v", DefinitionNames())
	}
	if !strings.Contains(SchemaText, "definition document {") {
		t.Fatal("SchemaText does not hold the schema")
	}
}
