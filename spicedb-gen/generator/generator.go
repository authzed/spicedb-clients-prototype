package generator

import "github.com/authzed/spicedb-clients/spicedb-gen/schema"

// GeneratedFile represents a single file produced by a language generator.
type GeneratedFile struct {
	Path    string
	Content []byte
}

// LanguageGenerator is the interface that language-specific code generators
// must implement.
type LanguageGenerator interface {
	// Language returns the name of the target language (e.g., "typescript").
	Language() string

	// Generate produces generated files from the parsed schema.
	Generate(s *schema.Schema) ([]GeneratedFile, error)
}

// Registry holds all registered language generators, keyed by language name.
var Registry = map[string]LanguageGenerator{}

// Register adds a language generator to the global registry.
func Register(g LanguageGenerator) {
	Registry[g.Language()] = g
}
