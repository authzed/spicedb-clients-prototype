package rel

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestFilterMatchesServerSemantics(t *testing.T) {
	const (
		direct  = "document:readme#viewer@user:alice"
		userset = "document:readme#viewer@team:eng#member"
		other   = "folder:root#viewer@user:alice"
	)
	members, _ := NamedRelation("member")
	admins, _ := NamedRelation("admin")

	cases := []struct {
		name   string
		filter Filter
		want   map[string]bool
	}{
		{"type only", MustNewFilter("document"), map[string]bool{direct: true, userset: true, other: false}},
		{"id", MustNewFilter("document").WithResourceID("readme"), map[string]bool{direct: true, userset: true}},
		{"wrong id", MustNewFilter("document").WithResourceID("other"), map[string]bool{direct: false}},
		{"prefix", MustNewFilter("document").WithResourceIDPrefix("read"), map[string]bool{direct: true}},
		{"wrong prefix", MustNewFilter("document").WithResourceIDPrefix("x"), map[string]bool{direct: false}},
		{"relation", MustNewFilter("document").WithRelation("editor"), map[string]bool{direct: false}},
		{"subject type", MustNewFilter("document").WithSubjectType("user"), map[string]bool{direct: true, userset: false}},
		{"subject id", MustNewFilter("document").WithSubjectType("user").WithSubjectID("bob"), map[string]bool{direct: false}},
		{"any relation", MustNewFilter("document").WithSubjectType("team").WithSubjectRelation(AnyRelation()), map[string]bool{userset: true}},
		{"direct relation", MustNewFilter("document").WithSubjectType("user").WithSubjectRelation(DirectRelation()), map[string]bool{direct: true}},
		{"direct excludes userset", MustNewFilter("document").WithSubjectType("team").WithSubjectRelation(DirectRelation()), map[string]bool{userset: false}},
		{"named", MustNewFilter("document").WithSubjectType("team").WithSubjectRelation(members), map[string]bool{userset: true}},
		{"named mismatch", MustNewFilter("document").WithSubjectType("team").WithSubjectRelation(admins), map[string]bool{userset: false}},
	}
	for _, tc := range cases {
		for tuple, want := range tc.want {
			require.Equal(t, want, tc.filter.Matches(MustFromTuple(tuple)), "%s: %s", tc.name, tuple)
		}
	}
	// A relationship's exact filter always matches it, regardless of caveats.
	d, u := MustFromTuple(direct), MustFromTuple(userset)
	caveated, err := d.WithCaveat("ip", map[string]any{"cidr": "1.2.3.4"})
	require.NoError(t, err)
	require.True(t, d.Filter().Matches(caveated.WithExpiration(time.Now())))
	require.True(t, u.Filter().Matches(u))
	require.False(t, u.Filter().Matches(d))
}

func TestMustConstructorsPanicOnInvalidInput(t *testing.T) {
	require.Panics(t, func() { MustNewObject("", "x") })
	require.Panics(t, func() { MustFromTuple("nope") })
	require.Panics(t, func() { MustNewFilter("") })
	require.NotPanics(t, func() {
		MustNewRelationship(MustNewObject("doc", "1"), "viewer", MustNewSubject(MustNewObject("user", "a"), ""))
	})
}

func TestShortAccessors(t *testing.T) {
	r := MustFromTuple("document:readme#viewer@team:eng#member")
	require.Equal(t, "document", r.ResourceType())
	require.Equal(t, "readme", r.ResourceID())
	require.Equal(t, "team", r.SubjectType())
	require.Equal(t, "eng", r.SubjectID())
	require.Equal(t, "member", r.SubjectRelation())
}

func TestNativeValuesAreObservable(t *testing.T) {
	var txn Txn
	require.Equal(t, 0, txn.Len())
	require.NoError(t, txn.MustNotMatch(MustNewFilter("document").WithRelation("owner")))
	require.NoError(t, txn.Touch(MustFromTuple("document:readme#viewer@user:alice")))
	require.Equal(t, 1, txn.Len())

	require.Equal(t, "[MUST_NOT_MATCH(document:*#owner@*), TOUCH(document:readme#viewer@user:alice)]", txn.String())
	require.Equal(t, "TOUCH(document:readme#viewer@user:alice)", fmt.Sprint(txn.Updates()[0]))
	require.Equal(t, "document:read%*#*@user:*#...", MustNewFilter("document").WithResourceIDPrefix("read").WithSubjectType("user").WithSubjectRelation(DirectRelation()).String())

	js, err := json.Marshal(txn.Updates())
	require.NoError(t, err)
	require.JSONEq(t, `[{"operation":"TOUCH","relationship":{"resource":"document:readme","relation":"viewer","subject":"user:alice"}}]`, string(js))
	js, err = json.Marshal(txn.Preconditions())
	require.NoError(t, err)
	require.JSONEq(t, `[{"operation":"MUST_NOT_MATCH","filter":"document:*#owner@*"}]`, string(js))

	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("write", "updates", txn.Updates(), "filter", MustNewFilter("document"))
	require.Contains(t, buf.String(), `"subject":"user:alice"`)
	require.Contains(t, buf.String(), `"filter":"document:*#*@*"`)
}

func TestErrFilterFailsClosed(t *testing.T) {
	f := ErrFilter(fmt.Errorf("boom"))
	require.EqualError(t, f.Validate(), "boom")
	require.False(t, f.Matches(MustFromTuple("document:readme#viewer@user:alice")))
	var txn Txn
	require.Error(t, txn.MustMatch(f))
	require.ErrorIs(t, ErrFilter(nil).Validate(), ErrInvalidFilter)
}
