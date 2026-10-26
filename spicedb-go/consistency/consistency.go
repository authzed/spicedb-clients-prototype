// Package consistency provides native, explicit read consistency and opaque revision tokens.
package consistency

import "fmt"

// ZedToken is an opaque revision value. Its zero value represents absence.
type ZedToken struct{ value string }

// NewZedToken preserves a serialized revision without interpreting its contents.
func NewZedToken(value string) ZedToken { return ZedToken{value: value} }

// String returns the exact serialized revision.
func (t ZedToken) String() string { return t.value }

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
	if token.String() == "" {
		return Full()
	}
	return AtLeast(token)
}
func AtLeastOrMinLatency(token ZedToken) Strategy {
	if token.String() == "" {
		return MinLatency()
	}
	return AtLeast(token)
}
func (s Strategy) Kind() Kind      { return s.kind }
func (s Strategy) Token() ZedToken { return s.token }
func (s Strategy) Validate() error {
	if s.kind == Invalid || ((s.kind == AtLeastAsFresh || s.kind == ExactSnapshot) && s.token.String() == "") {
		return fmt.Errorf("explicit consistency and nonempty revision token required")
	}
	return nil
}
