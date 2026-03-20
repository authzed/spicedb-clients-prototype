package java

import (
	"fmt"
	"strings"

	"github.com/authzed/spicedb-clients/spicedb-gen/generator"
	"github.com/authzed/spicedb-clients/spicedb-gen/schema"
)

func init() {
	generator.Register(&Generator{})
}

// Generator implements generator.LanguageGenerator for Java.
type Generator struct{}

// Language returns "java".
func (g *Generator) Language() string {
	return "java"
}

// Generate produces generated Java files from the parsed schema.
// opts must contain "package" specifying the Java package name.
func (g *Generator) Generate(s *schema.Schema, opts map[string]string) ([]generator.GeneratedFile, error) {
	pkg, ok := opts["package"]
	if !ok || pkg == "" {
		return nil, fmt.Errorf("java generator requires --java.package=<package> option")
	}

	_ = buildTemplateData(s, pkg)

	return []generator.GeneratedFile{
		{
			Path:    "Permissions.java",
			Content: []byte("// placeholder\npackage " + pkg + ";\n"),
		},
	}, nil
}

// TemplateData holds all pre-computed data needed by the Java template.
type TemplateData struct {
	Package            string
	CaveatContextTypes []CaveatContextData
	SubjectRefTypes    []SubjectRefTypeData
	TypeSentinels      []TypeSentinelData
	Definitions        []DefinitionData
	SealedInterfaces   []SealedInterfaceData
	PermissionVars     []PermissionVarData
}

// CaveatContextData holds data for generating a caveat context record.
type CaveatContextData struct {
	ClassName  string
	CaveatName string
	Params     []CaveatParamData
}

// CaveatParamData holds data for a single caveat parameter.
type CaveatParamData struct {
	FieldName  string
	ParamName  string
	JavaType   string
	WithMethod string
}

// SubjectRefTypeData holds data for a subject ref type.
type SubjectRefTypeData struct {
	TypeName       string
	DefinitionName string
	SubRelation    string
	CaveatName     string
	CaveatContext  string
	HasCaveat      bool
	HasExpiration  bool
	Interfaces     []string
	WithMethods    []WithMethodData
}

// WithMethodData holds data for a with* method on a base ref type.
type WithMethodData struct {
	MethodName  string
	ContextType string
	ReturnType  string
}

// TypeSentinelData holds data for a type sentinel.
type TypeSentinelData struct {
	ClassName      string
	VarName        string
	DefinitionName string
	SubRelation    string
	Interfaces     []string
}

// DefinitionData holds pre-computed data for a single definition.
type DefinitionData struct {
	Name          string
	PascalName    string
	IsSubject     bool
	Relations     []RelationData
	Permissions   []PermissionData
	SubRefMethods []SubRefMethodData
}

// RelationData holds data for a relation on a definition.
type RelationData struct {
	Name          string
	MethodName    string
	InterfaceName string
}

// PermissionData holds data for a permission on a definition.
type PermissionData struct {
	Name          string
	MethodName    string
	InterfaceName string
}

// SubRefMethodData holds data for a sub-ref accessor method on a definition ref.
type SubRefMethodData struct {
	MethodName string
	ReturnType string
}

// SealedInterfaceData holds data for generating a sealed interface.
type SealedInterfaceData struct {
	Name    string
	Permits []string
}

// PermissionVarData holds data for a static permission ref var.
type PermissionVarData struct {
	VarName        string
	InterfaceName  string
	ResourceType   string
	PermissionName string
}

// buildTemplateData pre-computes all data needed by the Java template.
// TODO: implement in next commit.
func buildTemplateData(s *schema.Schema, pkg string) TemplateData {
	return TemplateData{Package: pkg}
}

// toPascalCase converts a snake_case string to PascalCase.
func toPascalCase(s string) string {
	parts := strings.Split(s, "_")
	var result strings.Builder
	for _, p := range parts {
		if len(p) == 0 {
			continue
		}
		result.WriteString(strings.ToUpper(p[:1]))
		result.WriteString(p[1:])
	}
	return result.String()
}

// toLowerCamel converts a snake_case string to lowerCamelCase.
func toLowerCamel(s string) string {
	parts := strings.Split(s, "_")
	var result strings.Builder
	for i, p := range parts {
		if len(p) == 0 {
			continue
		}
		if i == 0 {
			result.WriteString(strings.ToLower(p[:1]))
			result.WriteString(p[1:])
		} else {
			result.WriteString(strings.ToUpper(p[:1]))
			result.WriteString(p[1:])
		}
	}
	return result.String()
}

// celTypeToJavaType maps CEL type strings to Java type strings.
func celTypeToJavaType(celType string) string {
	switch celType {
	case "string":
		return "String"
	case "int", "uint":
		return "Long"
	case "double":
		return "Double"
	case "bool":
		return "Boolean"
	case "duration", "timestamp":
		return "String"
	default:
		return "Object"
	}
}
