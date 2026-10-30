package client

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestEmptyPresharedKeyRejectedAtConstruction: an empty or whitespace-only
// key is a local validation error at construction, never a bare "Bearer"
// header the server rejects on the first call.
func TestEmptyPresharedKeyRejectedAtConstruction(t *testing.T) {
	ctors := map[string]func(key string) (*Client, error){
		"NewPlaintext": func(key string) (*Client, error) { return NewPlaintext("localhost:50116", key) },
		"NewSystemTLS": func(key string) (*Client, error) { return NewSystemTLS("localhost:50116", key) },
		"NewWithOpts":  func(key string) (*Client, error) { return NewWithOpts("localhost:50116", key, WithInsecure()) },
	}
	for name, ctor := range ctors {
		for _, key := range []string{"", " ", "\t\n"} {
			c, err := ctor(key)
			require.Nil(t, c, name)
			require.ErrorIs(t, err, ErrInvalidArgument, name)
			require.ErrorContains(t, err, "preshared key must not be empty or whitespace", name)
		}
	}
}
