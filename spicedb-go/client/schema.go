package client

import (
	"context"
	"fmt"

	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
	"github.com/authzed/spicedb-clients/spicedb-go/consistency"
)

// ReadSchema returns the current SpiceDB schema.
func (c *Client) ReadSchema(ctx context.Context) (schema string, revision string, err error) {
	resp, err := c.ssc.ReadSchema(ctx, &v1.ReadSchemaRequest{})
	if err != nil {
		return "", "", fmt.Errorf("spicedb: read schema: %w", err)
	}
	return resp.GetSchemaText(), resp.GetReadAt().GetToken(), nil
}

// WriteSchema writes a new schema to SpiceDB, returning the revision.
func (c *Client) WriteSchema(ctx context.Context, schema string) (revision string, err error) {
	resp, err := c.ssc.WriteSchema(ctx, &v1.WriteSchemaRequest{Schema: schema})
	if err != nil {
		return "", fmt.Errorf("spicedb: write schema: %w", err)
	}
	return resp.GetWrittenAt().GetToken(), nil
}

// SchemaDefinition represents a definition in a SpiceDB schema, including its
// relations and permissions.
type SchemaDefinition struct {
	Name        string
	Comment     string
	Relations   []SchemaRelation
	Permissions []SchemaPermission
}

// SchemaRelation represents a relation within a schema definition.
type SchemaRelation struct {
	Name                 string
	Comment              string
	ParentDefinitionName string
}

// SchemaPermission represents a permission within a schema definition.
type SchemaPermission struct {
	Name                 string
	Comment              string
	ParentDefinitionName string
}

// SchemaCaveat represents a caveat defined in a SpiceDB schema.
type SchemaCaveat struct {
	Name       string
	Comment    string
	Expression string
	Parameters []SchemaCaveatParameter
}

// SchemaCaveatParameter represents a parameter of a caveat.
type SchemaCaveatParameter struct {
	Name             string
	Type             string
	ParentCaveatName string
}

// ReflectSchemaResult holds the result of a schema reflection call.
type ReflectSchemaResult struct {
	Definitions []SchemaDefinition
	Caveats     []SchemaCaveat
	Revision    string
}

// ReflectSchema returns the definitions and caveats in the current schema.
func (c *Client) ReflectSchema(ctx context.Context, cs consistency.Strategy) (*ReflectSchemaResult, error) {
	resp, err := c.ssc.ReflectSchema(ctx, &v1.ReflectSchemaRequest{
		Consistency: cs.V1Consistency,
	})
	if err != nil {
		return nil, fmt.Errorf("spicedb: reflect schema: %w", err)
	}

	result := &ReflectSchemaResult{
		Revision: resp.GetReadAt().GetToken(),
	}

	for _, def := range resp.GetDefinitions() {
		sd := SchemaDefinition{
			Name:    def.GetName(),
			Comment: def.GetComment(),
		}
		for _, rel := range def.GetRelations() {
			sd.Relations = append(sd.Relations, SchemaRelation{
				Name:                 rel.GetName(),
				Comment:              rel.GetComment(),
				ParentDefinitionName: rel.GetParentDefinitionName(),
			})
		}
		for _, perm := range def.GetPermissions() {
			sd.Permissions = append(sd.Permissions, SchemaPermission{
				Name:                 perm.GetName(),
				Comment:              perm.GetComment(),
				ParentDefinitionName: perm.GetParentDefinitionName(),
			})
		}
		result.Definitions = append(result.Definitions, sd)
	}

	for _, cav := range resp.GetCaveats() {
		sc := SchemaCaveat{
			Name:       cav.GetName(),
			Comment:    cav.GetComment(),
			Expression: cav.GetExpression(),
		}
		for _, param := range cav.GetParameters() {
			sc.Parameters = append(sc.Parameters, SchemaCaveatParameter{
				Name:             param.GetName(),
				Type:             param.GetType(),
				ParentCaveatName: param.GetParentCaveatName(),
			})
		}
		result.Caveats = append(result.Caveats, sc)
	}

	return result, nil
}

// RelationReference identifies a relation or permission on a definition.
type RelationReference struct {
	DefinitionName string
	RelationName   string
	IsPermission   bool
}

// ComputablePermissions returns the permissions that are computable for the
// given relation on a definition.
func (c *Client) ComputablePermissions(ctx context.Context, cs consistency.Strategy, definitionName, relationName string) ([]RelationReference, string, error) {
	resp, err := c.ssc.ComputablePermissions(ctx, &v1.ComputablePermissionsRequest{
		Consistency:    cs.V1Consistency,
		DefinitionName: definitionName,
		RelationName:   relationName,
	})
	if err != nil {
		return nil, "", fmt.Errorf("spicedb: computable permissions: %w", err)
	}

	refs := make([]RelationReference, len(resp.GetPermissions()))
	for i, perm := range resp.GetPermissions() {
		refs[i] = RelationReference{
			DefinitionName: perm.GetDefinitionName(),
			RelationName:   perm.GetRelationName(),
			IsPermission:   perm.GetIsPermission(),
		}
	}
	return refs, resp.GetReadAt().GetToken(), nil
}

// DependentRelations returns the relations that the given permission depends on.
func (c *Client) DependentRelations(ctx context.Context, cs consistency.Strategy, definitionName, permissionName string) ([]RelationReference, string, error) {
	resp, err := c.ssc.DependentRelations(ctx, &v1.DependentRelationsRequest{
		Consistency:    cs.V1Consistency,
		DefinitionName: definitionName,
		PermissionName: permissionName,
	})
	if err != nil {
		return nil, "", fmt.Errorf("spicedb: dependent relations: %w", err)
	}

	refs := make([]RelationReference, len(resp.GetRelations()))
	for i, rel := range resp.GetRelations() {
		refs[i] = RelationReference{
			DefinitionName: rel.GetDefinitionName(),
			RelationName:   rel.GetRelationName(),
			IsPermission:   rel.GetIsPermission(),
		}
	}
	return refs, resp.GetReadAt().GetToken(), nil
}

// SchemaDiff represents a single difference between two schemas. The Kind
// field describes what changed, and the associated fields contain the details.
type SchemaDiff struct {
	// Kind is a human-readable description of the diff type (e.g.
	// "definition_added", "relation_removed", "permission_expr_changed").
	Kind string
	// DefinitionName is set for definition and relation/permission-level diffs.
	DefinitionName string
	// RelationName is set for relation-level diffs.
	RelationName string
	// PermissionName is set for permission-level diffs.
	PermissionName string
	// CaveatName is set for caveat-level diffs.
	CaveatName string
}

// DiffSchema compares the current schema against the given comparison schema,
// returning the differences.
func (c *Client) DiffSchema(ctx context.Context, cs consistency.Strategy, comparisonSchema string) ([]SchemaDiff, string, error) {
	resp, err := c.ssc.DiffSchema(ctx, &v1.DiffSchemaRequest{
		Consistency:      cs.V1Consistency,
		ComparisonSchema: comparisonSchema,
	})
	if err != nil {
		return nil, "", fmt.Errorf("spicedb: diff schema: %w", err)
	}

	var diffs []SchemaDiff
	for _, d := range resp.GetDiffs() {
		sd := schemaDiffFromProto(d)
		diffs = append(diffs, sd)
	}
	return diffs, resp.GetReadAt().GetToken(), nil
}

func schemaDiffFromProto(d *v1.ReflectionSchemaDiff) SchemaDiff {
	switch v := d.GetDiff().(type) {
	case *v1.ReflectionSchemaDiff_DefinitionAdded:
		return SchemaDiff{Kind: "definition_added", DefinitionName: v.DefinitionAdded.GetName()}
	case *v1.ReflectionSchemaDiff_DefinitionRemoved:
		return SchemaDiff{Kind: "definition_removed", DefinitionName: v.DefinitionRemoved.GetName()}
	case *v1.ReflectionSchemaDiff_DefinitionDocCommentChanged:
		return SchemaDiff{Kind: "definition_doc_comment_changed", DefinitionName: v.DefinitionDocCommentChanged.GetName()}
	case *v1.ReflectionSchemaDiff_RelationAdded:
		return SchemaDiff{Kind: "relation_added", DefinitionName: v.RelationAdded.GetParentDefinitionName(), RelationName: v.RelationAdded.GetName()}
	case *v1.ReflectionSchemaDiff_RelationRemoved:
		return SchemaDiff{Kind: "relation_removed", DefinitionName: v.RelationRemoved.GetParentDefinitionName(), RelationName: v.RelationRemoved.GetName()}
	case *v1.ReflectionSchemaDiff_RelationDocCommentChanged:
		return SchemaDiff{Kind: "relation_doc_comment_changed", DefinitionName: v.RelationDocCommentChanged.GetParentDefinitionName(), RelationName: v.RelationDocCommentChanged.GetName()}
	case *v1.ReflectionSchemaDiff_RelationSubjectTypeAdded:
		return SchemaDiff{Kind: "relation_subject_type_added", DefinitionName: v.RelationSubjectTypeAdded.GetRelation().GetParentDefinitionName(), RelationName: v.RelationSubjectTypeAdded.GetRelation().GetName()}
	case *v1.ReflectionSchemaDiff_RelationSubjectTypeRemoved:
		return SchemaDiff{Kind: "relation_subject_type_removed", DefinitionName: v.RelationSubjectTypeRemoved.GetRelation().GetParentDefinitionName(), RelationName: v.RelationSubjectTypeRemoved.GetRelation().GetName()}
	case *v1.ReflectionSchemaDiff_PermissionAdded:
		return SchemaDiff{Kind: "permission_added", DefinitionName: v.PermissionAdded.GetParentDefinitionName(), PermissionName: v.PermissionAdded.GetName()}
	case *v1.ReflectionSchemaDiff_PermissionRemoved:
		return SchemaDiff{Kind: "permission_removed", DefinitionName: v.PermissionRemoved.GetParentDefinitionName(), PermissionName: v.PermissionRemoved.GetName()}
	case *v1.ReflectionSchemaDiff_PermissionDocCommentChanged:
		return SchemaDiff{Kind: "permission_doc_comment_changed", DefinitionName: v.PermissionDocCommentChanged.GetParentDefinitionName(), PermissionName: v.PermissionDocCommentChanged.GetName()}
	case *v1.ReflectionSchemaDiff_PermissionExprChanged:
		return SchemaDiff{Kind: "permission_expr_changed", DefinitionName: v.PermissionExprChanged.GetParentDefinitionName(), PermissionName: v.PermissionExprChanged.GetName()}
	case *v1.ReflectionSchemaDiff_CaveatAdded:
		return SchemaDiff{Kind: "caveat_added", CaveatName: v.CaveatAdded.GetName()}
	case *v1.ReflectionSchemaDiff_CaveatRemoved:
		return SchemaDiff{Kind: "caveat_removed", CaveatName: v.CaveatRemoved.GetName()}
	case *v1.ReflectionSchemaDiff_CaveatDocCommentChanged:
		return SchemaDiff{Kind: "caveat_doc_comment_changed", CaveatName: v.CaveatDocCommentChanged.GetName()}
	case *v1.ReflectionSchemaDiff_CaveatExprChanged:
		return SchemaDiff{Kind: "caveat_expr_changed", CaveatName: v.CaveatExprChanged.GetName()}
	case *v1.ReflectionSchemaDiff_CaveatParameterAdded:
		return SchemaDiff{Kind: "caveat_parameter_added", CaveatName: v.CaveatParameterAdded.GetParentCaveatName()}
	case *v1.ReflectionSchemaDiff_CaveatParameterRemoved:
		return SchemaDiff{Kind: "caveat_parameter_removed", CaveatName: v.CaveatParameterRemoved.GetParentCaveatName()}
	case *v1.ReflectionSchemaDiff_CaveatParameterTypeChanged:
		return SchemaDiff{Kind: "caveat_parameter_type_changed", CaveatName: v.CaveatParameterTypeChanged.GetParameter().GetParentCaveatName()}
	default:
		return SchemaDiff{Kind: "unknown"}
	}
}
