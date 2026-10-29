package client

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
)

// Interface must list exactly the exported methods of *Client, so test
// doubles that implement it cover the whole client.
func TestInterfaceMatchesClient(t *testing.T) {
	iface := reflect.TypeFor[Interface]()
	impl := reflect.TypeFor[*Client]()
	require.Equal(t, impl.NumMethod(), iface.NumMethod(), "Interface and *Client must have the same methods")
	for i := 0; i < impl.NumMethod(); i++ {
		m := impl.Method(i)
		im, ok := iface.MethodByName(m.Name)
		require.True(t, ok, "Interface lacks %s", m.Name)
		// The interface method type excludes the receiver.
		want := m.Type
		require.Equal(t, want.NumIn()-1, im.Type.NumIn(), m.Name)
		for j := 1; j < want.NumIn(); j++ {
			require.Equal(t, want.In(j), im.Type.In(j-1), m.Name)
		}
		require.Equal(t, want.NumOut(), im.Type.NumOut(), m.Name)
		for j := 0; j < want.NumOut(); j++ {
			require.Equal(t, want.Out(j), im.Type.Out(j), m.Name)
		}
	}
}
