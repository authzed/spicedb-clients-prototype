// Package consistency provides native, explicit read consistency and opaque revision tokens.
package consistency

import (
	"fmt"
	"strings"
)

// ZedToken is an opaque revision value. Its zero value represents absence.
type ZedToken struct{ value string }

// NewZedToken preserves a serialized revision without interpreting its contents.
func NewZedToken(value string) ZedToken { return ZedToken{value: value} }

// String returns the exact serialized revision.
func (t ZedToken) String() string { return t.value }

// IsBlank reports whether the token is empty or whitespace-only. A blank
// token carries no revision: AtLeastOrFull and AtLeastOrMinLatency treat it
// as absent, and every API that requires a token rejects it as invalid.
func (t ZedToken) IsBlank() bool { return strings.TrimSpace(t.value) == "" }

type Kind uint8

const (
	Invalid Kind = iota
	FullyConsistent
	MinimizeLatency
	AtLeastAsFresh
	ExactSnapshot
)

type Strategy struct {
	kind  Kind
	token ZedToken
}

func Full() Strategy                   { return Strategy{kind: FullyConsistent} }
func MinLatency() Strategy             { return Strategy{kind: MinimizeLatency} }
func AtLeast(token ZedToken) Strategy  { return Strategy{kind: AtLeastAsFresh, token: token} }
func Snapshot(token ZedToken) Strategy { return Strategy{kind: ExactSnapshot, token: token} }
func AtLeastOrFull(token ZedToken) Strategy {
	if token.IsBlank() {
		return Full()
	}
	return AtLeast(token)
}
func AtLeastOrMinLatency(token ZedToken) Strategy {
	if token.IsBlank() {
		return MinLatency()
	}
	return AtLeast(token)
}
func (s Strategy) Kind() Kind      { return s.kind }
func (s Strategy) Token() ZedToken { return s.token }
func (s Strategy) Validate() error {
	if s.kind == Invalid || ((s.kind == AtLeastAsFresh || s.kind == ExactSnapshot) && s.token.IsBlank()) {
		return fmt.Errorf("explicit consistency and a non-blank revision token required")
	}
	return nil
}
