package java

import (
	"os"
	"testing"

	"github.com/authzed/spicedb-clients/spicedb-gen/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLanguage(t *testing.T) {
	g := &Generator{}
	assert.Equal(t, "java", g.Language())
}

func TestGenerateRequiresPackage(t *testing.T) {
	s := &schema.Schema{}
	g := &Generator{}

	_, err := g.Generate(s, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "package")

	_, err = g.Generate(s, map[string]string{})
	require.Error(t, err)

	_, err = g.Generate(s, map[string]string{"package": ""})
	require.Error(t, err)
}

func TestGenerate(t *testing.T) {
	s, err := schema.ParseFile("../testdata/sample.zed")
	require.NoError(t, err)

	gen := &Generator{}
	files, err := gen.Generate(s, map[string]string{"package": "com.authzed.spicedb.gen.test"})
	require.NoError(t, err)
	require.Len(t, files, 1)
	assert.Equal(t, "Permissions.java", files[0].Path)

	content := string(files[0].Content)

	// Write to /tmp for visual inspection.
	if writeErr := os.WriteFile("/tmp/Permissions.java", files[0].Content, 0644); writeErr != nil {
		t.Logf("warning: could not write to /tmp/Permissions.java: %v", writeErr)
	} else {
		t.Logf("wrote generated output to /tmp/Permissions.java")
	}

	// Print the generated output for visual review.
	t.Logf("Generated output:\n%s", content)

	// Check package declaration.
	assert.Contains(t, content, "package com.authzed.spicedb.gen.test;")

	// Check sealed Subject interface.
	assert.Contains(t, content, "public sealed interface Subject permits")

	// Check caveat context classes.
	assert.Contains(t, content, "public static final class IpRangeContext")
	assert.Contains(t, content, "public IpRangeContext withAllowedCidr(String v)")
	assert.Contains(t, content, "public static final class TimeWindowContext")

	// Check constraint sealed interfaces.
	assert.Contains(t, content, "public sealed interface DocumentViewerSubject extends Subject")
	assert.Contains(t, content, "public sealed interface DocumentEditorSubject extends Subject")
	assert.Contains(t, content, "public sealed interface DocumentViewSubject extends Subject")

	// Check base ref record.
	assert.Contains(t, content, "public record UserRef(String id) implements")
	assert.Contains(t, content, `return "user"`)

	// Check caveated ref record.
	assert.Contains(t, content, "public record UserIpRange(String id, IpRangeContext context) implements")
	assert.Contains(t, content, `return "ip_range"`)

	// Check sub-ref record.
	assert.Contains(t, content, "public record TeamMemberRef(String id) implements")

	// Check non-subject definition ref.
	assert.Contains(t, content, "public record DocumentRef(String id)")
	assert.Contains(t, content, "Permission<DocumentViewSubject> view()")

	// Check TeamRef with sub-ref method.
	assert.Contains(t, content, "public record TeamRef(String id)")
	assert.Contains(t, content, "public TeamMemberRef member()")

	// Check generic Permission types.
	assert.Contains(t, content, "public record Permission<S extends Subject>")
	assert.Contains(t, content, "public record PermissionRef<S extends Subject>")

	// Check static PermissionRef constants.
	assert.Contains(t, content, "Document_View")
	assert.Contains(t, content, "Document_Edit")
	assert.Contains(t, content, "Document_Delete")

	// Check type sentinels.
	assert.Contains(t, content, "public static final UserType UserType = new UserType()")
	assert.Contains(t, content, "public static final class UserType implements")
	assert.Contains(t, content, "public static final TeamMemberType TeamMemberType = new TeamMemberType()")

	// Check TypedRelationship record.
	assert.Contains(t, content, "public record TypedRelationship(")

	// Check factory methods.
	assert.Contains(t, content, "public static UserRef User(String id)")
	assert.Contains(t, content, "public static DocumentRef Document(String id)")
	assert.Contains(t, content, "public static TeamRef Team(String id)")

	// Check TypedClient class.
	assert.Contains(t, content, "public static final class TypedClient")
	assert.Contains(t, content, "public <S extends Subject> boolean check(")
	assert.Contains(t, content, "public <S extends Subject> Stream<String> lookupResources(")
	assert.Contains(t, content, "public <S extends Subject> Stream<String> lookupSubjects(")
	assert.Contains(t, content, "public String touch(TypedRelationship... rels)")
	assert.Contains(t, content, "public String create(TypedRelationship... rels)")
	assert.Contains(t, content, "public String delete(TypedRelationship... rels)")

	// Check relation methods produce TypedRelationship.
	assert.Contains(t, content, "public TypedRelationship viewer(DocumentViewerSubject subject)")
}

func TestBuildTemplateData(t *testing.T) {
	s, err := schema.ParseFile("../testdata/sample.zed")
	require.NoError(t, err)

	data := buildTemplateData(s, "com.example.permissions")

	// Package is set correctly.
	assert.Equal(t, "com.example.permissions", data.Package)

	// 2 caveat context types.
	require.Len(t, data.CaveatContextTypes, 2)
	caveatNames := []string{data.CaveatContextTypes[0].CaveatName, data.CaveatContextTypes[1].CaveatName}
	assert.Contains(t, caveatNames, "ip_range")
	assert.Contains(t, caveatNames, "time_window")

	// Verify ip_range caveat context.
	var ipRange CaveatContextData
	for _, c := range data.CaveatContextTypes {
		if c.CaveatName == "ip_range" {
			ipRange = c
			break
		}
	}
	assert.Equal(t, "IpRangeContext", ipRange.ClassName)
	require.Len(t, ipRange.Params, 1)
	assert.Equal(t, "allowedCidr", ipRange.Params[0].FieldName)
	assert.Equal(t, "allowed_cidr", ipRange.Params[0].ParamName)
	assert.Equal(t, "String", ipRange.Params[0].JavaType)
	assert.Equal(t, "withAllowedCidr", ipRange.Params[0].WithMethod)

	// Verify time_window caveat context.
	var timeWindow CaveatContextData
	for _, c := range data.CaveatContextTypes {
		if c.CaveatName == "time_window" {
			timeWindow = c
			break
		}
	}
	assert.Equal(t, "TimeWindowContext", timeWindow.ClassName)
	require.Len(t, timeWindow.Params, 2)

	// Subject ref types include expected types.
	refTypeNames := make([]string, len(data.SubjectRefTypes))
	for i, srt := range data.SubjectRefTypes {
		refTypeNames[i] = srt.TypeName
	}
	assert.Contains(t, refTypeNames, "UserRef")
	assert.Contains(t, refTypeNames, "UserIpRange")
	assert.Contains(t, refTypeNames, "UserTimeWindow")
	assert.Contains(t, refTypeNames, "TeamMemberRef")

	// Type sentinels include expected types.
	sentinelNames := make([]string, len(data.TypeSentinels))
	for i, ts := range data.TypeSentinels {
		sentinelNames[i] = ts.VarName
	}
	assert.Contains(t, sentinelNames, "UserType")
	assert.Contains(t, sentinelNames, "TeamMemberType")

	// Sealed interfaces include both relation and permission interfaces.
	ifaceNames := make([]string, len(data.SealedInterfaces))
	for i, si := range data.SealedInterfaces {
		ifaceNames[i] = si.Name
	}
	assert.Contains(t, ifaceNames, "DocumentViewerSubject")
	assert.Contains(t, ifaceNames, "DocumentEditorSubject")
	assert.Contains(t, ifaceNames, "DocumentOwnerSubject")
	assert.Contains(t, ifaceNames, "DocumentViewSubject")
	assert.Contains(t, ifaceNames, "DocumentEditSubject")
	assert.Contains(t, ifaceNames, "DocumentDeleteSubject")
	assert.Contains(t, ifaceNames, "TeamMemberSubject")

	// Subject sealed interface permits include sub-interfaces.
	var subjectIface SealedInterfaceData
	for _, si := range data.SealedInterfaces {
		if si.Name == "Subject" {
			subjectIface = si
			break
		}
	}
	require.NotEmpty(t, subjectIface.Permits, "Subject sealed interface should have permits")
	assert.Contains(t, subjectIface.Permits, "DocumentViewerSubject")
	assert.Contains(t, subjectIface.Permits, "DocumentViewSubject")
	assert.Contains(t, subjectIface.Permits, "UserRef")
	assert.Contains(t, subjectIface.Permits, "TeamMemberRef")

	// UserRef implements all expected interfaces.
	var userRef SubjectRefTypeData
	for _, srt := range data.SubjectRefTypes {
		if srt.TypeName == "UserRef" {
			userRef = srt
			break
		}
	}
	assert.Contains(t, userRef.Interfaces, "DocumentViewerSubject")
	assert.Contains(t, userRef.Interfaces, "DocumentEditorSubject")
	assert.Contains(t, userRef.Interfaces, "DocumentOwnerSubject")
	assert.Contains(t, userRef.Interfaces, "DocumentViewSubject")
	assert.Contains(t, userRef.Interfaces, "DocumentEditSubject")
	assert.Contains(t, userRef.Interfaces, "DocumentDeleteSubject")

	// Permission vars include expected entries.
	permVarNames := make([]string, len(data.PermissionVars))
	for i, pv := range data.PermissionVars {
		permVarNames[i] = pv.VarName
	}
	assert.Contains(t, permVarNames, "Document_View")
	assert.Contains(t, permVarNames, "Document_Edit")
	assert.Contains(t, permVarNames, "Document_Delete")

	// Team definition has forMember relation and member sub-ref.
	var teamDef DefinitionData
	for _, d := range data.Definitions {
		if d.Name == "team" {
			teamDef = d
			break
		}
	}
	require.Len(t, teamDef.Relations, 1)
	assert.Equal(t, "member", teamDef.Relations[0].Name)
	assert.Equal(t, "forMember", teamDef.Relations[0].MethodName, "relation method should be renamed due to sub-ref conflict")

	require.Len(t, teamDef.SubRefMethods, 1)
	assert.Equal(t, "member", teamDef.SubRefMethods[0].MethodName)
	assert.Equal(t, "TeamMemberRef", teamDef.SubRefMethods[0].ReturnType)

	// Verify DocumentViewerSubject permits include both ref types and sentinel types.
	var viewerIface SealedInterfaceData
	for _, si := range data.SealedInterfaces {
		if si.Name == "DocumentViewerSubject" {
			viewerIface = si
			break
		}
	}
	assert.Contains(t, viewerIface.Permits, "UserRef")
	assert.Contains(t, viewerIface.Permits, "UserIpRange")
	assert.Contains(t, viewerIface.Permits, "UserTimeWindow")
	assert.Contains(t, viewerIface.Permits, "TeamMemberRef")
	assert.Contains(t, viewerIface.Permits, "UserType")
	assert.Contains(t, viewerIface.Permits, "TeamMemberType")
}

func TestToPascalCase(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"user", "User"},
		{"document", "Document"},
		{"team_member", "TeamMember"},
		{"ip_range", "IpRange"},
		{"time_window", "TimeWindow"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, toPascalCase(tt.input))
		})
	}
}

func TestToLowerCamel(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"user", "user"},
		{"team_member", "teamMember"},
		{"ip_range", "ipRange"},
		{"allowed_cidr", "allowedCidr"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, toLowerCamel(tt.input))
		})
	}
}

func TestCelTypeToJavaType(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"string", "String"},
		{"int", "Long"},
		{"uint", "Long"},
		{"double", "Double"},
		{"bool", "Boolean"},
		{"duration", "String"},
		{"timestamp", "String"},
		{"unknown_type", "Object"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, celTypeToJavaType(tt.input))
		})
	}
}

func TestJavaSubjectRefTypeName(t *testing.T) {
	assert.Equal(t, "UserRef", javaSubjectRefTypeName(schema.SubjectType{Definition: "user"}))
	assert.Equal(t, "TeamMemberRef", javaSubjectRefTypeName(schema.SubjectType{Definition: "team", Relation: "member"}))
	assert.Equal(t, "UserIpRange", javaSubjectRefTypeName(schema.SubjectType{Definition: "user", CaveatName: "ip_range"}))
	assert.Equal(t, "UserExpiring", javaSubjectRefTypeName(schema.SubjectType{Definition: "user", Expiration: true}))
}
