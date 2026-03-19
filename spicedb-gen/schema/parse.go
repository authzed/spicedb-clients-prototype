package schema

import (
	"fmt"
	"os"
	"sort"

	core "github.com/authzed/spicedb/pkg/proto/core/v1"
	"github.com/authzed/spicedb/pkg/schemadsl/compiler"
	"github.com/authzed/spicedb/pkg/schemadsl/input"
)

// ParseFile reads a .zed schema file from disk and parses it into a Schema.
func ParseFile(path string) (*Schema, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading schema file: %w", err)
	}
	return ParseString(string(data))
}

// ParseString parses a SpiceDB schema string into a Schema.
func ParseString(schemaText string) (*Schema, error) {
	compiled, err := compiler.Compile(
		compiler.InputSchema{
			Source:       input.Source("schema"),
			SchemaString: schemaText,
		},
		compiler.AllowUnprefixedObjectType(),
	)
	if err != nil {
		return nil, fmt.Errorf("compiling schema: %w", err)
	}

	// Build a lookup map from definition name to its compiled relations,
	// so we can resolve cross-definition arrows.
	defMap := make(map[string]*core.NamespaceDefinition, len(compiled.ObjectDefinitions))
	for _, ns := range compiled.ObjectDefinitions {
		defMap[ns.GetName()] = ns
	}

	// Extract caveat definitions.
	caveats := make([]CaveatDefinition, 0, len(compiled.CaveatDefinitions))
	for _, cd := range compiled.CaveatDefinitions {
		caveat := CaveatDefinition{Name: cd.GetName()}
		for name, typeRef := range cd.GetParameterTypes() {
			caveat.Params = append(caveat.Params, CaveatParam{
				Name: name,
				Type: celTypeToString(typeRef),
			})
		}
		// Sort params by name for deterministic output.
		sort.Slice(caveat.Params, func(i, j int) bool {
			return caveat.Params[i].Name < caveat.Params[j].Name
		})
		caveats = append(caveats, caveat)
	}
	// Sort caveats by name for deterministic output.
	sort.Slice(caveats, func(i, j int) bool {
		return caveats[i].Name < caveats[j].Name
	})

	schema := &Schema{
		Definitions: make([]Definition, 0, len(compiled.ObjectDefinitions)),
		Caveats:     caveats,
	}

	for _, ns := range compiled.ObjectDefinitions {
		def := Definition{Name: ns.GetName()}

		for _, rel := range ns.GetRelation() {
			if rel.GetUsersetRewrite() != nil {
				// This is a permission (has a rewrite expression).
				subjects := collectRewriteSubjects(rel.GetUsersetRewrite(), ns, defMap)
				subjects = dedup(subjects)
				def.Permissions = append(def.Permissions, Permission{
					Name:              rel.GetName(),
					ReachableSubjects: subjects,
				})
			} else {
				// This is a relation (no rewrite, just allowed direct relations).
				def.Relations = append(def.Relations, Relation{
					Name:            rel.GetName(),
					AllowedSubjects: extractAllowedSubjects(rel),
				})
			}
		}

		schema.Definitions = append(schema.Definitions, def)
	}

	return schema, nil
}

// extractAllowedSubjects converts the AllowedDirectRelations on a relation
// into our SubjectType model.
func extractAllowedSubjects(rel *core.Relation) []SubjectType {
	ti := rel.GetTypeInformation()
	if ti == nil {
		return nil
	}

	subjects := make([]SubjectType, 0, len(ti.GetAllowedDirectRelations()))
	for _, allowed := range ti.GetAllowedDirectRelations() {
		st := SubjectType{
			Definition: allowed.GetNamespace(),
		}
		if allowed.GetPublicWildcard() != nil {
			st.Wildcard = true
		} else {
			rel := allowed.GetRelation()
			if rel != "..." {
				st.Relation = rel
			}
		}
		if reqCaveat := allowed.GetRequiredCaveat(); reqCaveat != nil {
			st.CaveatName = reqCaveat.GetCaveatName()
		}
		if allowed.GetRequiredExpiration() != nil {
			st.Expiration = true
		}
		subjects = append(subjects, st)
	}
	return subjects
}

// collectRewriteSubjects walks a UsersetRewrite tree and returns all
// reachable subject types.
func collectRewriteSubjects(
	rewrite *core.UsersetRewrite,
	currentDef *core.NamespaceDefinition,
	defMap map[string]*core.NamespaceDefinition,
) []SubjectType {
	if rewrite == nil {
		return nil
	}

	switch {
	case rewrite.GetUnion() != nil:
		return collectSetOpSubjects(rewrite.GetUnion(), currentDef, defMap, setOpUnion)
	case rewrite.GetIntersection() != nil:
		// For safety, treat intersection like union for reachability.
		return collectSetOpSubjects(rewrite.GetIntersection(), currentDef, defMap, setOpUnion)
	case rewrite.GetExclusion() != nil:
		return collectSetOpSubjects(rewrite.GetExclusion(), currentDef, defMap, setOpExclusion)
	}

	return nil
}

type setOpMode int

const (
	setOpUnion     setOpMode = iota
	setOpExclusion setOpMode = iota
)

// collectSetOpSubjects collects subjects from a SetOperation's children.
func collectSetOpSubjects(
	op *core.SetOperation,
	currentDef *core.NamespaceDefinition,
	defMap map[string]*core.NamespaceDefinition,
	mode setOpMode,
) []SubjectType {
	var result []SubjectType

	for i, child := range op.GetChild() {
		childSubjects := collectChildSubjects(child, currentDef, defMap)
		if mode == setOpExclusion && i > 0 {
			// For exclusion, only the base (first child) contributes reachable subjects.
			continue
		}
		result = append(result, childSubjects...)
	}

	return result
}

// collectChildSubjects collects subjects from a single SetOperation child.
func collectChildSubjects(
	child *core.SetOperation_Child,
	currentDef *core.NamespaceDefinition,
	defMap map[string]*core.NamespaceDefinition,
) []SubjectType {
	switch {
	case child.GetComputedUserset() != nil:
		// References another relation/permission on the same definition.
		relName := child.GetComputedUserset().GetRelation()
		return subjectsForRelation(relName, currentDef, defMap)

	case child.GetTupleToUserset() != nil:
		// Arrow: tupleset_relation->computed_relation
		ttu := child.GetTupleToUserset()
		tuplesetRelName := ttu.GetTupleset().GetRelation()
		computedRelName := ttu.GetComputedUserset().GetRelation()
		return subjectsForArrow(tuplesetRelName, computedRelName, currentDef, defMap)

	case child.GetFunctionedTupleToUserset() != nil:
		fttu := child.GetFunctionedTupleToUserset()
		tuplesetRelName := fttu.GetTupleset().GetRelation()
		computedRelName := fttu.GetComputedUserset().GetRelation()
		return subjectsForArrow(tuplesetRelName, computedRelName, currentDef, defMap)

	case child.GetUsersetRewrite() != nil:
		// Nested rewrite expression.
		return collectRewriteSubjects(child.GetUsersetRewrite(), currentDef, defMap)

	case child.GetXThis() != nil || child.GetXSelf() != nil:
		// _this / _self references the relation's own allowed direct types.
		// In a permission context this is unusual but we handle it for completeness.
		return nil
	}

	return nil
}

// subjectsForRelation returns the reachable subjects for a named relation or
// permission on the given definition.
func subjectsForRelation(
	relName string,
	def *core.NamespaceDefinition,
	defMap map[string]*core.NamespaceDefinition,
) []SubjectType {
	for _, rel := range def.GetRelation() {
		if rel.GetName() == relName {
			if rel.GetUsersetRewrite() != nil {
				// It's a permission — recurse into its rewrite.
				return collectRewriteSubjects(rel.GetUsersetRewrite(), def, defMap)
			}
			// It's a relation — return its allowed subjects.
			return extractAllowedSubjects(rel)
		}
	}
	return nil
}

// subjectsForArrow follows a tupleset->computed arrow:
//  1. Find the tupleset relation's allowed types on the current definition.
//  2. For each allowed type, look up the computed relation on that type's definition.
//  3. Collect all reachable subjects.
func subjectsForArrow(
	tuplesetRelName string,
	computedRelName string,
	currentDef *core.NamespaceDefinition,
	defMap map[string]*core.NamespaceDefinition,
) []SubjectType {
	// Find the tupleset relation to get its allowed types.
	var tuplesetRel *core.Relation
	for _, rel := range currentDef.GetRelation() {
		if rel.GetName() == tuplesetRelName {
			tuplesetRel = rel
			break
		}
	}
	if tuplesetRel == nil {
		return nil
	}

	ti := tuplesetRel.GetTypeInformation()
	if ti == nil {
		return nil
	}

	var result []SubjectType
	for _, allowed := range ti.GetAllowedDirectRelations() {
		targetDefName := allowed.GetNamespace()
		targetDef, ok := defMap[targetDefName]
		if !ok {
			continue
		}
		// Look up the computed relation on the target definition.
		subjects := subjectsForRelation(computedRelName, targetDef, defMap)
		result = append(result, subjects...)
	}

	return result
}

// dedup removes duplicate SubjectType entries.
func dedup(subjects []SubjectType) []SubjectType {
	type key struct {
		def        string
		rel        string
		wildcard   bool
		caveatName string
		expiration bool
	}
	seen := make(map[key]struct{}, len(subjects))
	result := make([]SubjectType, 0, len(subjects))
	for _, s := range subjects {
		k := key{s.Definition, s.Relation, s.Wildcard, s.CaveatName, s.Expiration}
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		result = append(result, s)
	}
	return result
}

// celTypeToString converts a CaveatTypeReference to its string type name.
func celTypeToString(ref *core.CaveatTypeReference) string {
	if ref == nil {
		return "any"
	}
	return ref.GetTypeName()
}
