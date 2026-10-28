package client

import (
	"context"

	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
	"github.com/authzed/spicedb-clients/spicedb-go/consistency"
)

type ReadSchemaRequest struct{}
type ReadSchemaResult struct {
	Schema   string
	Revision consistency.ZedToken
}
type WriteSchemaRequest struct{ Schema string }
type WriteSchemaResult struct{ Revision consistency.ZedToken }

func (c *Client) ReadSchema(ctx context.Context, request ReadSchemaRequest, opts ...ReadSchemaOption) (ReadSchemaResult, error) {
	o := operationOptions{}
	for _, opt := range opts {
		opt(&o)
	}
	ctx, cancel, e := c.operation(ctx, o, false)
	if e != nil {
		return ReadSchemaResult{}, e
	}
	defer cancel()
	resp, e := retry(ctx, c, "read schema", func() (*v1.ReadSchemaResponse, error) {
		return c.ssc.ReadSchema(ctx, &v1.ReadSchemaRequest{}, o.callOptions...)
	})
	if e != nil {
		return ReadSchemaResult{}, e
	}
	return ReadSchemaResult{resp.GetSchemaText(), consistency.NewZedToken(resp.GetReadAt().GetToken())}, nil
}
func (c *Client) WriteSchema(ctx context.Context, request WriteSchemaRequest, opts ...WriteSchemaOption) (WriteSchemaResult, error) {
	o := operationOptions{}
	for _, opt := range opts {
		opt(&o)
	}
	ctx, cancel, e := c.operation(ctx, o, false)
	if e != nil {
		return WriteSchemaResult{}, e
	}
	defer cancel()
	resp, e := c.ssc.WriteSchema(ctx, &v1.WriteSchemaRequest{Schema: request.Schema}, o.callOptions...)
	if e != nil {
		return WriteSchemaResult{}, mapGRPCError("write schema", e)
	}
	return WriteSchemaResult{consistency.NewZedToken(resp.GetWrittenAt().GetToken())}, nil
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
	SubjectTypes         []SchemaTypeReference
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
	Revision    consistency.ZedToken
}

// ReflectSchema returns the definitions and caveats in the current schema.
type ReflectSchemaRequest struct{ Consistency consistency.Strategy }

func (c *Client) ReflectSchema(ctx context.Context, request ReflectSchemaRequest, opts ...ReflectSchemaOption) (ReflectSchemaResult, error) {
	o := operationOptions{}
	for _, opt := range opts {
		opt(&o)
	}
	ctx, cancel, e := c.operation(ctx, o, false)
	if e != nil {
		return ReflectSchemaResult{}, e
	}
	defer cancel()
	cs, e := strategy(request.Consistency)
	if e != nil {
		return ReflectSchemaResult{}, e
	}
	resp, err := retry(ctx, c, "reflect schema", func() (*v1.ReflectSchemaResponse, error) {
		return c.ssc.ReflectSchema(ctx, &v1.ReflectSchemaRequest{Consistency: cs, OptionalFilters: schemaFilters(o.schemaFilters)}, o.callOptions...)
	})
	if err != nil {
		return ReflectSchemaResult{}, err
	}

	result := ReflectSchemaResult{
		Revision: consistency.NewZedToken(resp.GetReadAt().GetToken()),
	}

	for _, def := range resp.GetDefinitions() {
		result.Definitions = append(result.Definitions, nativeDefinition(def))
	}
	for _, caveat := range resp.GetCaveats() {
		result.Caveats = append(result.Caveats, nativeCaveat(caveat))
	}

	return result, nil
}

// RelationReference identifies a relation or permission on a definition.
type RelationReference struct {
	DefinitionName string
	RelationName   string
	IsPermission   bool
}

type ComputablePermissionsRequest struct {
	DefinitionName, RelationName string
	Consistency                  consistency.Strategy
}
type ComputablePermissionsResult struct {
	Permissions []RelationReference
	Revision    consistency.ZedToken
}
type DependentRelationsRequest struct {
	DefinitionName, PermissionName string
	Consistency                    consistency.Strategy
}
type DependentRelationsResult struct {
	Relations []RelationReference
	Revision  consistency.ZedToken
}

func nativeRelationReferences(ps []*v1.ReflectionRelationReference) []RelationReference {
	out := make([]RelationReference, len(ps))
	for i, p := range ps {
		out[i] = RelationReference{p.GetDefinitionName(), p.GetRelationName(), p.GetIsPermission()}
	}
	return out
}
func (c *Client) ComputablePermissions(ctx context.Context, request ComputablePermissionsRequest, opts ...ComputablePermissionsOption) (ComputablePermissionsResult, error) {
	o := operationOptions{}
	for _, opt := range opts {
		opt(&o)
	}
	ctx, cancel, e := c.operation(ctx, o, false)
	if e != nil {
		return ComputablePermissionsResult{}, e
	}
	defer cancel()
	cs, e := strategy(request.Consistency)
	if e != nil {
		return ComputablePermissionsResult{}, e
	}
	if request.DefinitionName == "" || request.RelationName == "" {
		return ComputablePermissionsResult{}, invalid("definition and relation required")
	}
	resp, e := retry(ctx, c, "computable permissions", func() (*v1.ComputablePermissionsResponse, error) {
		return c.ssc.ComputablePermissions(ctx, &v1.ComputablePermissionsRequest{Consistency: cs, DefinitionName: request.DefinitionName, RelationName: request.RelationName, OptionalDefinitionNameFilter: o.definitionFilter}, o.callOptions...)
	})
	if e != nil {
		return ComputablePermissionsResult{}, e
	}
	return ComputablePermissionsResult{nativeRelationReferences(resp.GetPermissions()), consistency.NewZedToken(resp.GetReadAt().GetToken())}, nil
}
func (c *Client) DependentRelations(ctx context.Context, request DependentRelationsRequest, opts ...DependentRelationsOption) (DependentRelationsResult, error) {
	o := operationOptions{}
	for _, opt := range opts {
		opt(&o)
	}
	ctx, cancel, e := c.operation(ctx, o, false)
	if e != nil {
		return DependentRelationsResult{}, e
	}
	defer cancel()
	cs, e := strategy(request.Consistency)
	if e != nil {
		return DependentRelationsResult{}, e
	}
	if request.DefinitionName == "" || request.PermissionName == "" {
		return DependentRelationsResult{}, invalid("definition and permission required")
	}
	resp, e := retry(ctx, c, "dependent relations", func() (*v1.DependentRelationsResponse, error) {
		return c.ssc.DependentRelations(ctx, &v1.DependentRelationsRequest{Consistency: cs, DefinitionName: request.DefinitionName, PermissionName: request.PermissionName}, o.callOptions...)
	})
	if e != nil {
		return DependentRelationsResult{}, e
	}
	return DependentRelationsResult{nativeRelationReferences(resp.GetRelations()), consistency.NewZedToken(resp.GetReadAt().GetToken())}, nil
}

// SchemaDiff represents a single difference between two schemas. The Kind
// field describes what changed, and the associated fields contain the details.
type SchemaDiff struct {
	Definition         *SchemaDefinition
	Relation           *SchemaRelation
	Permission         *SchemaPermission
	Caveat             *SchemaCaveat
	Parameter          *SchemaCaveatParameter
	ChangedSubjectType *SchemaTypeReference
	PreviousType       string
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

type DiffSchemaRequest struct {
	ComparisonSchema string
	Consistency      consistency.Strategy
}
type DiffSchemaResult struct {
	Diffs    []SchemaDiff
	Revision consistency.ZedToken
}

func (c *Client) DiffSchema(ctx context.Context, request DiffSchemaRequest, opts ...DiffSchemaOption) (DiffSchemaResult, error) {
	o := operationOptions{}
	for _, opt := range opts {
		opt(&o)
	}
	ctx, cancel, e := c.operation(ctx, o, false)
	if e != nil {
		return DiffSchemaResult{}, e
	}
	defer cancel()
	cs, e := strategy(request.Consistency)
	if e != nil {
		return DiffSchemaResult{}, e
	}
	resp, e := retry(ctx, c, "diff schema", func() (*v1.DiffSchemaResponse, error) {
		return c.ssc.DiffSchema(ctx, &v1.DiffSchemaRequest{Consistency: cs, ComparisonSchema: request.ComparisonSchema}, o.callOptions...)
	})
	if e != nil {
		return DiffSchemaResult{}, e
	}
	out := DiffSchemaResult{Revision: consistency.NewZedToken(resp.GetReadAt().GetToken())}
	for _, d := range resp.GetDiffs() {
		out.Diffs = append(out.Diffs, schemaDiffFromProto(d))
	}
	return out, nil
}

// SchemaTypeReference preserves the reflection type-reference variant and its presence.
type SchemaTypeReferenceKind uint8

const (
	SchemaTypeUnknown SchemaTypeReferenceKind = iota
	SchemaTypeTerminal
	SchemaTypeRelation
	SchemaTypeWildcard
)

type SchemaTypeReference struct {
	DefinitionName, CaveatName string
	Kind                       SchemaTypeReferenceKind
	Relation                   string
	Enabled                    bool
}

func nativeTypeReference(p *v1.ReflectionTypeReference) SchemaTypeReference {
	r := SchemaTypeReference{DefinitionName: p.GetSubjectDefinitionName(), CaveatName: p.GetOptionalCaveatName()}
	switch v := p.GetTyperef().(type) {
	case *v1.ReflectionTypeReference_IsTerminalSubject:
		r.Kind = SchemaTypeTerminal
		r.Enabled = v.IsTerminalSubject
	case *v1.ReflectionTypeReference_OptionalRelationName:
		r.Kind = SchemaTypeRelation
		r.Relation = v.OptionalRelationName
	case *v1.ReflectionTypeReference_IsPublicWildcard:
		r.Kind = SchemaTypeWildcard
		r.Enabled = v.IsPublicWildcard
	}
	return r
}
func nativeRelation(p *v1.ReflectionRelation) SchemaRelation {
	r := SchemaRelation{Name: p.GetName(), Comment: p.GetComment(), ParentDefinitionName: p.GetParentDefinitionName()}
	for _, subject := range p.GetSubjectTypes() {
		r.SubjectTypes = append(r.SubjectTypes, nativeTypeReference(subject))
	}
	return r
}
func nativePermission(p *v1.ReflectionPermission) SchemaPermission {
	return SchemaPermission{Name: p.GetName(), Comment: p.GetComment(), ParentDefinitionName: p.GetParentDefinitionName()}
}
func nativeParameter(p *v1.ReflectionCaveatParameter) SchemaCaveatParameter {
	return SchemaCaveatParameter{Name: p.GetName(), Type: p.GetType(), ParentCaveatName: p.GetParentCaveatName()}
}
func nativeDefinition(p *v1.ReflectionDefinition) SchemaDefinition {
	r := SchemaDefinition{Name: p.GetName(), Comment: p.GetComment()}
	for _, relation := range p.GetRelations() {
		r.Relations = append(r.Relations, nativeRelation(relation))
	}
	for _, permission := range p.GetPermissions() {
		r.Permissions = append(r.Permissions, nativePermission(permission))
	}
	return r
}
func nativeCaveat(p *v1.ReflectionCaveat) SchemaCaveat {
	r := SchemaCaveat{Name: p.GetName(), Comment: p.GetComment(), Expression: p.GetExpression()}
	for _, parameter := range p.GetParameters() {
		r.Parameters = append(r.Parameters, nativeParameter(parameter))
	}
	return r
}
func schemaDiffFromProto(d *v1.ReflectionSchemaDiff) SchemaDiff {
	r := SchemaDiff{Kind: "unknown"}
	switch v := d.GetDiff().(type) {
	case *v1.ReflectionSchemaDiff_DefinitionAdded:
		r.Kind = "definition_added"
		value := nativeDefinition(v.DefinitionAdded)
		r.Definition = &value
	case *v1.ReflectionSchemaDiff_DefinitionRemoved:
		r.Kind = "definition_removed"
		value := nativeDefinition(v.DefinitionRemoved)
		r.Definition = &value
	case *v1.ReflectionSchemaDiff_DefinitionDocCommentChanged:
		r.Kind = "definition_doc_comment_changed"
		value := nativeDefinition(v.DefinitionDocCommentChanged)
		r.Definition = &value
	case *v1.ReflectionSchemaDiff_RelationAdded:
		r.Kind = "relation_added"
		value := nativeRelation(v.RelationAdded)
		r.Relation = &value
	case *v1.ReflectionSchemaDiff_RelationRemoved:
		r.Kind = "relation_removed"
		value := nativeRelation(v.RelationRemoved)
		r.Relation = &value
	case *v1.ReflectionSchemaDiff_RelationDocCommentChanged:
		r.Kind = "relation_doc_comment_changed"
		value := nativeRelation(v.RelationDocCommentChanged)
		r.Relation = &value
	case *v1.ReflectionSchemaDiff_PermissionAdded:
		r.Kind = "permission_added"
		value := nativePermission(v.PermissionAdded)
		r.Permission = &value
	case *v1.ReflectionSchemaDiff_PermissionRemoved:
		r.Kind = "permission_removed"
		value := nativePermission(v.PermissionRemoved)
		r.Permission = &value
	case *v1.ReflectionSchemaDiff_PermissionDocCommentChanged:
		r.Kind = "permission_doc_comment_changed"
		value := nativePermission(v.PermissionDocCommentChanged)
		r.Permission = &value
	case *v1.ReflectionSchemaDiff_PermissionExprChanged:
		r.Kind = "permission_expr_changed"
		value := nativePermission(v.PermissionExprChanged)
		r.Permission = &value
	case *v1.ReflectionSchemaDiff_CaveatAdded:
		r.Kind = "caveat_added"
		value := nativeCaveat(v.CaveatAdded)
		r.Caveat = &value
	case *v1.ReflectionSchemaDiff_CaveatRemoved:
		r.Kind = "caveat_removed"
		value := nativeCaveat(v.CaveatRemoved)
		r.Caveat = &value
	case *v1.ReflectionSchemaDiff_CaveatDocCommentChanged:
		r.Kind = "caveat_doc_comment_changed"
		value := nativeCaveat(v.CaveatDocCommentChanged)
		r.Caveat = &value
	case *v1.ReflectionSchemaDiff_CaveatExprChanged:
		r.Kind = "caveat_expr_changed"
		value := nativeCaveat(v.CaveatExprChanged)
		r.Caveat = &value
	case *v1.ReflectionSchemaDiff_CaveatParameterAdded:
		r.Kind = "caveat_parameter_added"
		value := nativeParameter(v.CaveatParameterAdded)
		r.Parameter = &value
	case *v1.ReflectionSchemaDiff_CaveatParameterRemoved:
		r.Kind = "caveat_parameter_removed"
		value := nativeParameter(v.CaveatParameterRemoved)
		r.Parameter = &value
	case *v1.ReflectionSchemaDiff_RelationSubjectTypeAdded:
		r.Kind = "relation_subject_type_added"
		relation := nativeRelation(v.RelationSubjectTypeAdded.GetRelation())
		r.Relation = &relation
		subject := nativeTypeReference(v.RelationSubjectTypeAdded.GetChangedSubjectType())
		r.ChangedSubjectType = &subject
	case *v1.ReflectionSchemaDiff_RelationSubjectTypeRemoved:
		r.Kind = "relation_subject_type_removed"
		relation := nativeRelation(v.RelationSubjectTypeRemoved.GetRelation())
		r.Relation = &relation
		subject := nativeTypeReference(v.RelationSubjectTypeRemoved.GetChangedSubjectType())
		r.ChangedSubjectType = &subject
	case *v1.ReflectionSchemaDiff_CaveatParameterTypeChanged:
		r.Kind = "caveat_parameter_type_changed"
		param := nativeParameter(v.CaveatParameterTypeChanged.GetParameter())
		r.Parameter = &param
		r.PreviousType = v.CaveatParameterTypeChanged.GetPreviousType()
	}
	if r.Definition != nil {
		r.DefinitionName = r.Definition.Name
	}
	if r.Relation != nil {
		r.DefinitionName = r.Relation.ParentDefinitionName
		r.RelationName = r.Relation.Name
	}
	if r.Permission != nil {
		r.DefinitionName = r.Permission.ParentDefinitionName
		r.PermissionName = r.Permission.Name
	}
	if r.Caveat != nil {
		r.CaveatName = r.Caveat.Name
	}
	if r.Parameter != nil {
		r.CaveatName = r.Parameter.ParentCaveatName
	}
	return r
}

type SchemaFilter struct{ DefinitionName, CaveatName, RelationName, PermissionName string }

func schemaFilters(filters []SchemaFilter) []*v1.ReflectionSchemaFilter {
	out := make([]*v1.ReflectionSchemaFilter, len(filters))
	for i, f := range filters {
		out[i] = &v1.ReflectionSchemaFilter{OptionalDefinitionNameFilter: f.DefinitionName, OptionalCaveatNameFilter: f.CaveatName, OptionalRelationNameFilter: f.RelationName, OptionalPermissionNameFilter: f.PermissionName}
	}
	return out
}
func WithReflectSchemaFilters(filters []SchemaFilter) ReflectSchemaOption {
	snapshot := append([]SchemaFilter(nil), filters...)
	return func(o *operationOptions) { o.schemaFilters = snapshot }
}
func WithComputablePermissionsDefinitionFilter(name string) ComputablePermissionsOption {
	return func(o *operationOptions) { o.definitionFilter = name }
}
