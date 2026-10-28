package golang

import (
	"go/format"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/authzed/spicedb-clients/spicedb-gen/schema"
)

func generate(t *testing.T, zed string, opts map[string]string) (string, error) {
	t.Helper()
	s, err := schema.ParseString(zed)
	require.NoError(t, err)
	files, err := (&Generator{}).Generate(s, opts)
	if err != nil {
		return "", err
	}
	return string(files[0].Content), nil
}

func TestPackageOptionAndFormatting(t *testing.T) {
	out, err := generate(t, "definition user {}\ndefinition doc {\n\trelation viewer: user\n}", map[string]string{"package": "authzperms"})
	require.NoError(t, err)
	assert.Contains(t, out, "package authzperms\n")
	formatted, err := format.Source([]byte(out))
	require.NoError(t, err)
	assert.Equal(t, string(formatted), out, "output must already be gofmt-clean")

	for _, bad := range []string{"", "1abc", "func", "my-pkg"} {
		_, err := generate(t, "definition user {}", map[string]string{"package": bad})
		assert.Error(t, err, bad)
	}
	_, err = generate(t, "definition user {}", map[string]string{"unknown": "x"})
	assert.Error(t, err)
}

// The schema from the adoption report: a relation whose subject is a userset
// on a permission used to reference an interface that was never emitted.
func TestPermissionUsersetSubject(t *testing.T) {
	out, err := generate(t, `
definition user {}
definition org {
	relation admin: user
	permission can_admin = admin
}
definition inv {
	relation grant: org#can_admin
}`, nil)
	require.NoError(t, err)
	out = squash(out)
	assert.Contains(t, out, "type OrgCanAdminRef struct")
	assert.Contains(t, out, "func (d OrgRef) CanAdminSubject() OrgCanAdminRef")
	assert.Contains(t, out, "func (d OrgRef) CanAdmin() Permission")
	assert.Contains(t, out, "type InvGrantSubject interface")
	assert.Contains(t, out, "func (OrgCanAdminRef) isInvGrantSubject() {}")
	assert.Contains(t, out, "var OrgCanAdminType = ")
}

func TestConflictingIdentifiersFail(t *testing.T) {
	// relation "viewer_relation" collides with the check accessor of "viewer".
	_, err := generate(t, `
definition user {}
definition doc {
	relation viewer: user
	relation viewer_relation: user
	permission view = viewer
}`, nil)
	require.ErrorContains(t, err, "ViewerRelation")
}
