package consistency_test

import (
	"encoding/json"
	"fmt"
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

// A strategy and a token are observable: a readable string form and a
// structured JSON form, never an empty object (root DESIGN.md, "Make native
// values observable and testable without the server").
func TestStrategyAndTokenStringAndJSONForms(t *testing.T) {
	token := consistency.NewZedToken("GhUKEzE2")
	cases := []struct {
		strategy consistency.Strategy
		str      string
		json     string
	}{
		{consistency.Full(), "Full", `{"strategy":"full"}`},
		{consistency.MinLatency(), "MinLatency", `{"strategy":"minLatency"}`},
		{consistency.AtLeast(token), "AtLeast(GhUKEzE2)", `{"strategy":"atLeast","token":"GhUKEzE2"}`},
		{consistency.Snapshot(token), "Snapshot(GhUKEzE2)", `{"strategy":"snapshot","token":"GhUKEzE2"}`},
		{consistency.AtLeastOrFull(token), "AtLeast(GhUKEzE2)", `{"strategy":"atLeast","token":"GhUKEzE2"}`},
		{consistency.AtLeastOrMinLatency(consistency.NewZedToken("")), "MinLatency", `{"strategy":"minLatency"}`},
		{consistency.Strategy{}, "Invalid", `{"strategy":"invalid"}`},
	}
	for _, tc := range cases {
		require.Equal(t, tc.str, tc.strategy.String())
		require.Equal(t, tc.str, fmt.Sprint(tc.strategy))
		got, err := json.Marshal(tc.strategy)
		require.NoError(t, err)
		require.JSONEq(t, tc.json, string(got))
	}

	got, err := json.Marshal(token)
	require.NoError(t, err)
	require.Equal(t, `"GhUKEzE2"`, string(got))
	var back consistency.ZedToken
	require.NoError(t, json.Unmarshal(got, &back))
	require.Equal(t, token, back)
}

func TestWhitespaceTokenIsBlank(t *testing.T) {
	for _, raw := range []string{"", " ", "\t\n "} {
		token := consistency.NewZedToken(raw)
		require.True(t, token.IsBlank(), "%q", raw)
		require.Error(t, consistency.AtLeast(token).Validate(), "%q", raw)
		require.Error(t, consistency.Snapshot(token).Validate(), "%q", raw)
		require.Equal(t, consistency.FullyConsistent, consistency.AtLeastOrFull(token).Kind(), "%q", raw)
		require.Equal(t, consistency.MinimizeLatency, consistency.AtLeastOrMinLatency(token).Kind(), "%q", raw)
	}
	require.False(t, consistency.NewZedToken(" opaque ").IsBlank(), "a token is otherwise kept verbatim")
	require.Equal(t, " opaque ", consistency.NewZedToken(" opaque ").String())
}
