package schema

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReachingSubjectPositions(t *testing.T) {
	s, err := ParseFile("../testdata/sample.zed")
	require.NoError(t, err)
	r := s.ReachingSubjectPositions()
	require.Equal(t, []string{"team#member", "user"}, r["document#view"], "team#member expands to its members")
	require.Equal(t, []string{"team#member", "user"}, r["document#viewer"], "relations are positions too")
	require.Equal(t, []string{"user"}, r["document#editor"])
	require.Equal(t, []string{"user"}, r["organization#creator"], "a wildcard contributes its type")
	require.Equal(t, []string{"organization#can_admin", "user"}, r["invitation#accept"], "permission subject sets expand")
	require.Equal(t, []string{"organization"}, r["invitation#org"])
	require.Equal(t, []string{"team#member", "organization#can_admin"}, s.SubjectSetPositions())

	rules := s.SubjectRules()
	for _, want := range []string{
		"document#viewer@user", "document#viewer@user[ip_range]", "document#viewer@team#member",
		"organization#creator@user:*", "invitation#timed_grant@organization#can_admin[expiration]",
		"invitation#inviter@user[expiration]",
	} {
		require.Contains(t, rules, want)
	}
	require.NotContains(t, rules, "invitation#inviter@user")
}

func TestReachingSubjectPositionsTerminatesOnCycles(t *testing.T) {
	s, err := ParseString(`definition user {}
definition group {
    relation member: user | group#member | group#admin
    relation admin: user | group#member
}`)
	require.NoError(t, err)
	require.Equal(t, []string{"group#admin", "group#member", "user"}, s.ReachingSubjectPositions()["group#admin"])
}
