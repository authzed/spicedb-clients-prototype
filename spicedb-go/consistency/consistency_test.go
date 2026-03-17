package consistency_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/authzed/spicedb-clients/spicedb-go/consistency"
)

func TestFull(t *testing.T) {
	s := consistency.Full()
	require.NotNil(t, s.V1Consistency)
	require.NotNil(t, s.V1Consistency.GetRequirement())
}

func TestMinLatency(t *testing.T) {
	s := consistency.MinLatency()
	require.NotNil(t, s.V1Consistency)
	require.NotNil(t, s.V1Consistency.GetRequirement())
}

func TestAtLeast(t *testing.T) {
	s := consistency.AtLeast("zedtoken123")
	require.NotNil(t, s.V1Consistency)
	require.NotNil(t, s.V1Consistency.GetRequirement())
}

func TestAtLeastOrFull_WithRevision(t *testing.T) {
	s := consistency.AtLeastOrFull("zedtoken123")
	require.NotNil(t, s.V1Consistency)
	require.NotNil(t, s.V1Consistency.GetAtLeastAsFresh())
	require.Equal(t, "zedtoken123", s.V1Consistency.GetAtLeastAsFresh().GetToken())
}

func TestAtLeastOrFull_Empty(t *testing.T) {
	s := consistency.AtLeastOrFull("")
	require.NotNil(t, s.V1Consistency)
	require.True(t, s.V1Consistency.GetFullyConsistent())
}

func TestAtLeastOrMinLatency_WithRevision(t *testing.T) {
	s := consistency.AtLeastOrMinLatency("zedtoken123")
	require.NotNil(t, s.V1Consistency)
	require.NotNil(t, s.V1Consistency.GetAtLeastAsFresh())
	require.Equal(t, "zedtoken123", s.V1Consistency.GetAtLeastAsFresh().GetToken())
}

func TestAtLeastOrMinLatency_Empty(t *testing.T) {
	s := consistency.AtLeastOrMinLatency("")
	require.NotNil(t, s.V1Consistency)
	require.True(t, s.V1Consistency.GetMinimizeLatency())
}

func TestSnapshot(t *testing.T) {
	s := consistency.Snapshot("zedtoken456")
	require.NotNil(t, s.V1Consistency)
	require.NotNil(t, s.V1Consistency.GetRequirement())
}
