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
	// opts contains language-specific options parsed from --<lang>.<key>=<value> CLI flags.
	Generate(s *schema.Schema, opts map[string]string) ([]GeneratedFile, error)
}

// Option describes one language-specific option, passed as
// --<language>.<Name>=<Value>.
type Option struct {
	Name        string // e.g. "package"
	Value       string // placeholder for the value, e.g. "<name>"
	Description string
}

// OptionDescriber is implemented by generators that accept options, so the
// CLI can list them in its help.
type OptionDescriber interface {
	Options() []Option
}

// Registry holds all registered language generators, keyed by language name.
var Registry = map[string]LanguageGenerator{}

// Register adds a language generator to the global registry.
func Register(g LanguageGenerator) {
	Registry[g.Language()] = g
}
