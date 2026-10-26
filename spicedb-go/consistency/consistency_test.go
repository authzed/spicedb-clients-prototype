package consistency_test

import (
	"testing"

	"github.com/authzed/spicedb-clients/spicedb-go/consistency"
	"github.com/stretchr/testify/require"
)

func TestStrategiesAndOpaqueTokens(t *testing.T) {
	token := consistency.NewZedToken("opaque:+/===")
	require.Equal(t, "opaque:+/===", token.String())
	for _, s := range []consistency.Strategy{consistency.Full(), consistency.MinLatency(), consistency.AtLeast(token), consistency.Snapshot(token), consistency.AtLeastOrFull(consistency.NewZedToken("")), consistency.AtLeastOrMinLatency(consistency.NewZedToken(""))} {
		require.NoError(t, s.Validate())
	}
	require.Equal(t, consistency.FullyConsistent, consistency.AtLeastOrFull(consistency.NewZedToken("")).Kind())
	require.Equal(t, consistency.MinimizeLatency, consistency.AtLeastOrMinLatency(consistency.NewZedToken("")).Kind())
	require.Equal(t, token, consistency.AtLeastOrFull(token).Token())
	require.Equal(t, token, consistency.AtLeastOrMinLatency(token).Token())
	for _, s := range []consistency.Strategy{{}, consistency.AtLeast(consistency.NewZedToken("")), consistency.Snapshot(consistency.NewZedToken(""))} {
		require.Error(t, s.Validate())
	}
}
