// Package consistency provides constructors for SpiceDB consistency strategies.
//
// Every read operation on the SpiceDB client requires an explicit consistency
// strategy. This prevents silent defaults and makes consistency choices visible.
package consistency

import (
	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
)

// Strategy represents a consistency requirement for read operations.
// Use the constructor functions in this package to create a Strategy.
type Strategy struct {
	// V1Consistency exposes the underlying proto type for advanced use cases.
	V1Consistency *v1.Consistency
}

// Full returns a strategy that requires full consistency. This is the least
// performant option but guarantees the most up-to-date results.
func Full() Strategy {
	return Strategy{
		V1Consistency: &v1.Consistency{
			Requirement: &v1.Consistency_FullyConsistent{FullyConsistent: true},
		},
	}
}

// MinLatency returns a strategy that uses SpiceDB's preferred revision for
// optimal performance. This is the recommended default for most read operations.
func MinLatency() Strategy {
	return Strategy{
		V1Consistency: &v1.Consistency{
			Requirement: &v1.Consistency_MinimizeLatency{MinimizeLatency: true},
		},
	}
}

// AtLeast returns a strategy that ensures results are at least as fresh as the
// given revision. Use this for read-after-write consistency.
func AtLeast(revision string) Strategy {
	return Strategy{
		V1Consistency: &v1.Consistency{
			Requirement: &v1.Consistency_AtLeastAsFresh{
				AtLeastAsFresh: &v1.ZedToken{Token: revision},
			},
		},
	}
}

// AtLeastOrFull returns AtLeast(revision) if revision is non-empty,
// otherwise Full(). Use this when you have an optional revision from a
// previous write and want the safest fallback.
func AtLeastOrFull(revision string) Strategy {
	if revision == "" {
		return Full()
	}
	return AtLeast(revision)
}

// AtLeastOrMinLatency returns AtLeast(revision) if revision is non-empty,
// otherwise MinLatency(). Use this when you have an optional revision but
// prefer performance over full consistency as the fallback.
func AtLeastOrMinLatency(revision string) Strategy {
	if revision == "" {
		return MinLatency()
	}
	return AtLeast(revision)
}

// Snapshot returns a strategy that reads at the exact given revision.
func Snapshot(revision string) Strategy {
	return Strategy{
		V1Consistency: &v1.Consistency{
			Requirement: &v1.Consistency_AtExactSnapshot{
				AtExactSnapshot: &v1.ZedToken{Token: revision},
			},
		},
	}
}
