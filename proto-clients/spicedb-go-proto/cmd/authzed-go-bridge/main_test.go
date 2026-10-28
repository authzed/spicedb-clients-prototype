package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestBridgeLinksBesideCanonicalPackages builds the generated bridge as a
// stand-in legacy package and runs a binary that links it beside the
// canonical packages. A second generated copy would panic in init with
// "proto: file ... is already registered"; the bridge must not, and its types
// must be the canonical types.
func TestBridgeLinksBesideCanonicalPackages(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a module")
	}
	here, err := filepath.Abs("../..")
	require.NoError(t, err)
	repo, err := filepath.Abs("../../../..")
	require.NoError(t, err)

	dir := t.TempDir()
	for _, rel := range bridged {
		src, err := Generate(canonicalPrefix + rel)
		require.NoError(t, err)
		pkgDir := filepath.Join(dir, "legacy", filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(pkgDir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(pkgDir, "zz_generated.bridge.go"), src, 0o644))
	}

	gomod := `module bridgetest

go 1.26.6

require github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto v0.0.0

replace github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto => ` + here + `

replace github.com/authzed/spicedb-clients => ` + repo + `
`
	main := `package main

import (
	"fmt"

	canonical "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
	_ "bridgetest/legacy/authzed/api/materialize/v0"
	legacy "bridgetest/legacy/authzed/api/v1"
)

func main() {
	var req *canonical.CheckPermissionRequest = &legacy.CheckPermissionRequest{}
	req.Resource = &legacy.ObjectReference{ObjectType: "document", ObjectId: "*"}
	b, err := req.MarshalVT()
	if err != nil {
		panic(err)
	}
	var back legacy.CheckPermissionRequest
	if err := back.UnmarshalVT(b); err != nil {
		panic(err)
	}
	fmt.Println(back.GetResource().GetObjectType(), back.HandwrittenValidate() != nil, legacy.File_authzed_api_v1_core_proto.Path())
}
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte(gomod), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte(main), 0o644))

	cmd := exec.Command("go", "run", ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	require.Equal(t, "document true authzed/api/v1/core.proto", strings.TrimSpace(lastLine(string(out))))
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}

func TestIsBridgedFile(t *testing.T) {
	for name, want := range map[string]bool{
		"core.pb.go": true, "core_vtproto.pb.go": true, "core.pb.validate.go": true,
		"permission_service.pb.gw.go": true, "00_handwritten_validation.go": true,
		"zz_generated.version.go": false, "zz_generated.bridge.go": false,
	} {
		require.Equal(t, want, IsBridgedFile(name), name)
	}
}
