package python

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLanguage(t *testing.T) {
	g := &Generator{}
	assert.Equal(t, "python", g.Language())
}

func TestToPascalCase(t *testing.T) {
	for input, expected := range map[string]string{
		"user":         "User",
		"team_member":  "TeamMember",
		"ip_range":     "IpRange",
		"time_window":  "TimeWindow",
		"already":      "Already",
	} {
		assert.Equal(t, expected, toPascalCase(input), "input=%q", input)
	}
}

func TestToSnakeCase(t *testing.T) {
	for input, expected := range map[string]string{
		"user":          "user",
		"team_member":   "team_member",
		"TeamMember":    "team_member",
		"CamelCase":     "camel_case",
		"with_already":  "with_already",
	} {
		assert.Equal(t, expected, toSnakeCase(input), "input=%q", input)
	}
}

func TestEscapeKeyword(t *testing.T) {
	// Python keywords get a trailing underscore.
	for _, kw := range []string{"class", "def", "del", "from", "if", "for", "return", "type"} {
		assert.Equal(t, kw+"_", escapeKeyword(kw))
	}
	// Non-keywords pass through.
	assert.Equal(t, "viewer", escapeKeyword("viewer"))
	assert.Equal(t, "edit", escapeKeyword("edit"))
}

func TestCelTypeToPyType(t *testing.T) {
	for input, expected := range map[string]string{
		"string":       "str",
		"int":          "int",
		"uint":         "int",
		"double":       "float",
		"bool":         "bool",
		"duration":     "str",
		"timestamp":    "str",
		"unknown_type": "Any",
	} {
		assert.Equal(t, expected, celTypeToPyType(input), "input=%q", input)
	}
}
