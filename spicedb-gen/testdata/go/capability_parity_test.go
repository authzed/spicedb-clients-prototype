package permissions_test

import (
	. "github.com/authzed/spicedb-clients/spicedb-gen/testdata/go"
	"github.com/authzed/spicedb-clients/spicedb-go/client"
	"github.com/stretchr/testify/require"
	"reflect"
	"testing"
)

// Every native method must remain directly callable, with identical arguments,
// variadic options and results. New native capabilities automatically participate.
func TestTypedClientNativeCapabilityParity(t *testing.T) {
	native, typed := reflect.TypeFor[*client.Client](), reflect.TypeFor[*TypedClient]()
	for i := 0; i < native.NumMethod(); i++ {
		method := native.Method(i)
		t.Run(method.Name, func(t *testing.T) {
			forwarded, ok := typed.MethodByName(method.Name)
			require.True(t, ok, "missing native capability %s", method.Name)
			require.Equal(t, method.Type.NumIn(), forwarded.Type.NumIn())
			require.Equal(t, method.Type.NumOut(), forwarded.Type.NumOut())
			require.Equal(t, method.Type.IsVariadic(), forwarded.Type.IsVariadic())
			for j := 1; j < method.Type.NumIn(); j++ {
				require.Equal(t, method.Type.In(j), forwarded.Type.In(j))
			}
			for j := 0; j < method.Type.NumOut(); j++ {
				require.Equal(t, method.Type.Out(j), forwarded.Type.Out(j))
			}
		})
	}
}
