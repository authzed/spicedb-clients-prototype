package schema

// Schema represents a parsed SpiceDB schema containing all definitions.
type Schema struct {
	Definitions []Definition
}

// Definition represents a single object definition in a SpiceDB schema.
type Definition struct {
	Name        string
	Relations   []Relation
	Permissions []Permission
}

// Relation represents a relation on a definition, with its allowed subject types.
type Relation struct {
	Name            string
	AllowedSubjects []SubjectType
}

// SubjectType represents a type that can appear as a subject in a relation.
type SubjectType struct {
	Definition string // e.g., "user"
	Relation   string // e.g., "member" — empty means the object itself
	Wildcard   bool   // true for user:*
}

// Permission represents a permission on a definition, with the subject types
// that can be reached through the permission's expression tree.
type Permission struct {
	Name              string
	ReachableSubjects []SubjectType
}
