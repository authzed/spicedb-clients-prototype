package client

import (
	"context"
	"github.com/authzed/spicedb-clients/spicedb-go/rel"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestNamedOptionModesValidateBeforeRPC(t *testing.T) {
	c := &Client{}
	for _, mode := range []CheckTracing{TracingDisabled, TracingEnabled} {
		o := operationOptions{}
		WithCheckTracing(mode)(&o)
		require.NoError(t, o.err)
		require.Equal(t, mode == TracingEnabled, o.tracing)
	}
	_, err := c.CheckBulk(context.Background(), nil, WithCheckTracing(CheckTracing(255)))
	require.ErrorIs(t, err, ErrInvalidArgument)
	o := operationOptions{}
	WithLookupResourcesDebug(LookupDebug(255))(&o)
	require.ErrorIs(t, o.err, ErrInvalidArgument)
	o = operationOptions{}
	WithUpdatesCheckpoints(WatchCheckpoints(255))(&o)
	require.ErrorIs(t, o.err, ErrInvalidArgument)
	for _, mode := range []LookupDebug{DebugDisabled, DebugEnabled} {
		o = operationOptions{}
		WithLookupResourcesDebug(mode)(&o)
		require.NoError(t, o.err)
		require.Equal(t, mode == DebugEnabled, o.debug)
	}
	for _, mode := range []WatchCheckpoints{CheckpointsExcluded, CheckpointsIncluded} {
		o = operationOptions{}
		WithUpdatesCheckpoints(mode)(&o)
		require.NoError(t, o.err)
		require.Equal(t, mode == CheckpointsIncluded, o.checkpoints)
	}
}

func TestAllConstructorsExposeRemotePlaintextPolicy(t *testing.T) {
	constructors := []struct {
		name string
		new  func(string, string, ...Option) (*Client, error)
		opts []Option
	}{
		{"plaintext", NewPlaintext, nil}, {"options", NewWithOpts, []Option{WithInsecure()}}, {"tls configurable", NewSystemTLS, []Option{WithInsecure()}},
	}
	for _, ctor := range constructors {
		t.Run(ctor.name, func(t *testing.T) {
			for _, host := range []string{"remote.example:50051", "192.0.2.1:50051"} {
				_, err := ctor.new(host, "secret", ctor.opts...)
				require.ErrorIs(t, err, ErrInvalidArgument)
				opts := append(append([]Option{}, ctor.opts...), WithRemotePlaintextCredentials(RemotePlaintextAllowed))
				c, err := ctor.new(host, "secret", opts...)
				require.NoError(t, err)
				require.NoError(t, c.Close())
				opts = append(append([]Option{}, ctor.opts...), WithRemotePlaintextCredentials(RemotePlaintextCredentials(255)))
				_, err = ctor.new(host, "secret", opts...)
				require.ErrorIs(t, err, ErrInvalidArgument)
			}
		})
	}
}

func TestPreconditionModesRejectUnknown(t *testing.T) {
	_, err := preconditions([]rel.Precondition{{Filter: testFilter("document"), Operation: rel.PreconditionOperation(255)}})
	require.ErrorIs(t, err, ErrInvalidArgument)
}
