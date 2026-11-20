// Package lookupbatch computes the response batch sizes LookupResources
// requests. The client and the clienttest double share it so they batch alike.
package lookupbatch

// MaxDefault caps the default batch size: SpiceDB rejects a larger one unless
// its maximum was raised.
const MaxDefault = 1000

// Default is the batch size a call requests when none is configured: the page
// size, or in single-request mode the total limit (MaxDefault without one),
// capped at MaxDefault.
func Default(pageSize uint32, limit uint64, singleRequest bool) uint32 {
	if !singleRequest {
		return min(pageSize, MaxDefault)
	}
	if limit == 0 {
		return MaxDefault
	}
	return uint32(min(limit, MaxDefault))
}

// ForPage is the batch size the next page request asks for, 0 meaning one
// resource per response. batchSize is the call's batch size (0 when batching
// is disabled) and yielded counts the results the call has yielded so far.
//
// A batch has one cursor, after its last result, so a call that stops at its
// total limit in the middle of a batch would end on an item without a cursor
// and could not be resumed from there. The page that reaches the limit
// therefore asks for a batch size that puts a batch boundary on the limit, or
// for unbatched responses when none does. Results dropped client-side make
// that boundary unpredictable, so a limited call that filters is unbatched.
// Single-request mode needs none of this: the server stops at the limit and
// ends its last batch there.
func ForPage(batchSize, pageSize uint32, limit, yielded uint64, singleRequest, filtered bool) uint32 {
	if batchSize == 0 || limit == 0 || singleRequest {
		return batchSize
	}
	if filtered {
		return 0
	}
	remaining := limit - min(yielded, limit)
	if remaining >= uint64(pageSize) || remaining%uint64(batchSize) == 0 {
		return batchSize
	}
	if remaining < uint64(batchSize) {
		return uint32(remaining)
	}
	return 0
}
