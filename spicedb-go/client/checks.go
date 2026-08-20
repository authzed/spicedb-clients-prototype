package client

import (
	"context"
	"fmt"
	"iter"

	"google.golang.org/grpc/status"

	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
	"github.com/authzed/spicedb-clients/spicedb-go/consistency"
	"github.com/authzed/spicedb-clients/spicedb-go/rel"
)

// defaultCheckBatchSize bounds how many items go into a single
// CheckBulkPermissions request.
//
// SpiceDB rejects a request carrying more items than maxBulkCheckCount --
// 10,000, a hard-coded const in internal/services/v1/bulkcheck.go with no
// flag to raise or lower it -- with
// ERROR_REASON_TOO_MANY_CHECKS_IN_REQUEST. Nothing in the proto enforces
// this: CheckBulkPermissionsRequest.items carries only a per-item
// `required` rule, not a collection-size rule, so the limit lives solely in
// server code and a client that forwards the caller's slice unchanged fails
// on large inputs. 1,000 leaves ten times' headroom and matches
// defaultImportBatchSize.
//
// One constant deliberately serves two callers: CheckWithContext chunks a
// caller's slice by it, and CheckIter accumulates into batches of it. They
// are the same RPC against the same server limit, so a second constant
// could only ever drift out of agreement with this one. Because CheckIter
// flushes at exactly this size, each batch it hands to CheckWithContext is
// a single chunk there -- CheckIter's request pattern is unchanged by the
// chunking below.
const defaultCheckBatchSize = 1000

// Check performs a bulk permission check on the given relationships and
// returns a CheckResult for each relationship, in the same order. All checks
// use BulkCheckPermissions under the hood.
//
// Large inputs are split automatically into requests of at most 1,000 items
// and the responses concatenated in input order -- SpiceDB rejects a single
// request carrying more than 10,000. An empty rs sends no request at all.
// Results within one request share a CheckedAt (the response carries a
// single token for the whole batch, not one per item), so an input large
// enough to be split can carry more than one token across the returned
// slice.
//
// CheckResult.Permissionship carries the server's three-valued answer — a
// Conditional result means the server needed caveat context that was not
// supplied, and is NOT a grant. Prefer CheckResult.HasPermission() over
// comparing Permissionship directly for the common case.
//
// See CheckWithContext to supply a call-level caveat context for evaluating
// caveats encountered during the check; a relationship built with
// rel.Relationship.WithCheckContext still supplies its own per-item context
// through this method (Check is CheckWithContext with a nil call-level
// context).
func (c *Client) Check(ctx context.Context, cs consistency.Strategy, permission string, rs ...rel.Relationship) ([]CheckResult, error) {
	return c.CheckWithContext(ctx, cs, permission, nil, rs...)
}

// CheckWithContext is Check with a call-level default caveat context applied
// to every relationship in rs. Caveat context supplies named values (e.g.
// "now") that SpiceDB needs to evaluate a caveat expression encountered
// during the check; without it, a caveated match comes back as
// CheckResult.Permissionship == PermissionshipConditionalPermission instead
// of a grant, and CheckResult.MissingContext names what was needed.
//
// A relationship built with rel.Relationship.WithCheckContext overrides
// checkContext on a per-key basis for that one relationship: the item's keys
// win on conflict, and any call-level keys the item doesn't specify are
// retained. For example, a call-level {"now": 42, "region": "us"} plus a
// per-item {"region": "eu"} sends {"now": 42, "region": "eu"} for that item,
// while a sibling item with no per-item context still gets the untouched
// call-level default. Pass a nil checkContext for no call-level default
// (equivalent to calling Check).
func (c *Client) CheckWithContext(ctx context.Context, cs consistency.Strategy, permission string, checkContext map[string]any, rs ...rel.Relationship) ([]CheckResult, error) {
	// Zero relationships sends nothing at all. An empty request is not a
	// cheaper way to ask nothing -- it is a round trip whose only possible
	// answer is the empty slice, and CheckAllWithContext already treats an
	// aggregate over zero checks as false rather than a grant.
	if len(rs) == 0 {
		return nil, nil
	}

	// One request per chunk of defaultCheckBatchSize, results concatenated
	// in input order so results[i] still corresponds to rs[i] across the
	// chunk boundary. A caller passing fewer than defaultCheckBatchSize
	// relationships -- the overwhelmingly common case -- still makes
	// exactly one request.
	results := make([]CheckResult, 0, len(rs))
	for start := 0; start < len(rs); start += defaultCheckBatchSize {
		end := min(start+defaultCheckBatchSize, len(rs))
		chunkResults, err := c.checkChunk(ctx, cs, permission, checkContext, rs[start:end], start)
		if err != nil {
			return nil, err
		}
		results = append(results, chunkResults...)
	}
	return results, nil
}

// checkChunk issues one CheckBulkPermissions request for rs and maps the
// response. rs must be non-empty and no longer than defaultCheckBatchSize;
// CheckWithContext is what enforces both. Every response guard below --
// the pair-count check and the malformed-oneof check -- therefore applies
// per chunk, exactly as it applied to the whole request before chunking.
//
// offset is rs's start index within the caller's full slice. Every "check
// item %d" below reports offset+i, not i: the index a caller sees must be
// the one they can use to look up their own relationship. Reporting the
// chunk-relative index would attribute the failing item to a different
// resource entirely -- the same misattribution the pair-count guard exists
// to prevent, relocated into the diagnostic.
func (c *Client) checkChunk(ctx context.Context, cs consistency.Strategy, permission string, checkContext map[string]any, rs []rel.Relationship, offset int) ([]CheckResult, error) {
	items := make([]*v1.CheckBulkPermissionsRequestItem, len(rs))
	for i, r := range rs {
		item, err := checkItemFromRel(r, permission, checkContext)
		if err != nil {
			return nil, err
		}
		items[i] = item
	}

	resp, err := c.psc.CheckBulkPermissions(ctx, &v1.CheckBulkPermissionsRequest{
		Consistency: cs.V1Consistency,
		Items:       items,
	})
	if err != nil {
		return nil, mapGRPCError("check", err)
	}

	// The proto guarantees pairs are returned in request order but says
	// nothing about count. A short response would otherwise silently desync
	// results[i] from rs[i] for every item after the gap -- one resource's
	// answer attributed to another -- and CheckOneWithContext's results[0]
	// would panic with index-out-of-range on a zero-pair response. Fail
	// loudly instead of returning a misaligned-but-"successful" slice.
	if len(resp.GetPairs()) != len(items) {
		return nil, &Error{
			Code: CodeInternal,
			Message: fmt.Sprintf(
				"spicedb: check: CheckBulkPermissions returned %d pair(s) for %d request item(s)",
				len(resp.GetPairs()), len(items),
			),
		}
	}

	checkedAt := resp.GetCheckedAt().GetToken()
	results := make([]CheckResult, len(resp.GetPairs()))
	for i, pair := range resp.GetPairs() {
		if errResp := pair.GetError(); errResp != nil {
			return nil, mapGRPCError(fmt.Sprintf("check item %d", offset+i), status.FromProto(errResp).Err())
		}
		if pair.GetItem() == nil {
			// pair.Response is a oneof -- a well-behaved server always sets
			// it to Item or Error, so this should be unreachable in
			// practice. Mirrors spicedb-rust's malformed-oneof guard: fail
			// loudly instead of falling through to the item's zero value,
			// which reads as PERMISSIONSHIP_UNSPECIFIED.
			return nil, &Error{
				Code: CodeInternal,
				Message: fmt.Sprintf(
					"spicedb: check item %d: malformed CheckBulkPermissionsPair (neither item nor error set)",
					offset+i,
				),
			}
		}
		results[i] = checkResultFromBulkItem(pair.GetItem(), checkedAt)
	}
	return results, nil
}

// CheckOne checks a single permission and returns its CheckResult.
func (c *Client) CheckOne(ctx context.Context, cs consistency.Strategy, permission string, r rel.Relationship) (CheckResult, error) {
	return c.CheckOneWithContext(ctx, cs, permission, nil, r)
}

// CheckOneWithContext is CheckOne with a call-level default caveat context
// (see CheckWithContext for the merge rule with any per-item context on r).
func (c *Client) CheckOneWithContext(ctx context.Context, cs consistency.Strategy, permission string, checkContext map[string]any, r rel.Relationship) (CheckResult, error) {
	results, err := c.CheckWithContext(ctx, cs, permission, checkContext, r)
	if err != nil {
		return CheckResult{}, err
	}
	// Safe to index: CheckWithContext is called with exactly one
	// relationship, and it now guarantees one result per request item or an
	// error. Before that guard a zero-pair response reached this line and
	// panicked with index-out-of-range.
	return results[0], nil
}

// CheckAny returns true if any of the given relationships have the
// permission outright. A Conditional result does not count as granted — only
// CheckResult.HasPermission() results are considered.
func (c *Client) CheckAny(ctx context.Context, cs consistency.Strategy, permission string, rs ...rel.Relationship) (bool, error) {
	return c.CheckAnyWithContext(ctx, cs, permission, nil, rs...)
}

// CheckAnyWithContext is CheckAny with a call-level default caveat context
// (see CheckWithContext for the merge rule with any per-item context).
func (c *Client) CheckAnyWithContext(ctx context.Context, cs consistency.Strategy, permission string, checkContext map[string]any, rs ...rel.Relationship) (bool, error) {
	results, err := c.CheckWithContext(ctx, cs, permission, checkContext, rs...)
	if err != nil {
		return false, err
	}
	for _, r := range results {
		if r.HasPermission() {
			return true, nil
		}
	}
	return false, nil
}

// CheckAll returns true if all of the given relationships have the
// permission outright. A Conditional result does not count as granted — every
// result must satisfy CheckResult.HasPermission() for CheckAll to return true.
func (c *Client) CheckAll(ctx context.Context, cs consistency.Strategy, permission string, rs ...rel.Relationship) (bool, error) {
	return c.CheckAllWithContext(ctx, cs, permission, nil, rs...)
}

// CheckAllWithContext is CheckAll with a call-level default caveat context
// (see CheckWithContext for the merge rule with any per-item context).
//
// An empty rs returns false, not true: Go's bare for-loop aggregate below is
// vacuously true on zero relationships, and CheckAll must never treat "no
// checks to run" as "all checks passed".
func (c *Client) CheckAllWithContext(ctx context.Context, cs consistency.Strategy, permission string, checkContext map[string]any, rs ...rel.Relationship) (bool, error) {
	if len(rs) == 0 {
		return false, nil
	}

	results, err := c.CheckWithContext(ctx, cs, permission, checkContext, rs...)
	if err != nil {
		return false, err
	}
	for _, r := range results {
		if !r.HasPermission() {
			return false, nil
		}
	}
	return true, nil
}

// CheckIter checks permissions for relationships from an iterator, yielding
// CheckResults as they are computed. Relationships are automatically batched
// into chunks of 1,000 for efficient bulk checking.
func (c *Client) CheckIter(ctx context.Context, cs consistency.Strategy, permission string, rels iter.Seq[rel.Relationship]) iter.Seq2[CheckResult, error] {
	return c.CheckIterWithContext(ctx, cs, permission, nil, rels)
}

// CheckIterWithContext is CheckIter with a call-level default caveat context
// applied to every relationship in rels (see CheckWithContext for the merge
// rule with any per-item context).
func (c *Client) CheckIterWithContext(ctx context.Context, cs consistency.Strategy, permission string, checkContext map[string]any, rels iter.Seq[rel.Relationship]) iter.Seq2[CheckResult, error] {
	return func(yield func(CheckResult, error) bool) {
		batch := make([]rel.Relationship, 0, defaultCheckBatchSize)

		flush := func() bool {
			if len(batch) == 0 {
				return true
			}
			results, err := c.CheckWithContext(ctx, cs, permission, checkContext, batch...)
			// Clear the batch unconditionally, before yield runs, on both the
			// error and success paths. Go's iterator contract says yield
			// returning true means "keep going" -- a consumer that logs an
			// error and continues must start the next batch from empty, not
			// from the batch that just failed. Returning on the error path
			// before this line (the original bug) left the failed batch in
			// place: the next relationship pushed it over
			// defaultCheckBatchSize, so flush was called again with the SAME
			// failing batch plus one -- and again for every remaining
			// element, growing without bound until it crossed SpiceDB's
			// maxBulkCheckCount and the transient error became a permanent
			// InvalidArgument.
			batch = batch[:0]
			if err != nil {
				return yield(CheckResult{}, err)
			}
			for _, result := range results {
				if !yield(result, nil) {
					return false
				}
			}
			return true
		}

		for r := range rels {
			batch = append(batch, r)
			if len(batch) >= defaultCheckBatchSize {
				if !flush() {
					return
				}
			}
		}
		flush()
	}
}

// mergeCheckContext merges call-level and per-item check context per the
// key-level merge rule: item keys win on conflict, and call-level keys the
// item doesn't specify are retained. Returns nil (no context field to set)
// when both are empty.
func mergeCheckContext(callLevel, item map[string]any) map[string]any {
	if len(callLevel) == 0 && len(item) == 0 {
		return nil
	}
	merged := make(map[string]any, len(callLevel)+len(item))
	for k, v := range callLevel {
		merged[k] = v
	}
	for k, v := range item {
		merged[k] = v
	}
	return merged
}

// checkItemFromRel builds the wire item for r, merging call-level and
// per-item caveat context. Returns an error — instead of silently sending
// the item with no context — if the merged context cannot be converted to a
// protobuf Struct (rel.CaveatContextToStruct rejects values protobuf cannot
// represent), so a caller never mistakes "your context was dropped" for
// "the server needed more context than you supplied". The error names the
// offending key and wraps rel.ErrInvalidCaveatContext.
func checkItemFromRel(r rel.Relationship, permission string, callLevelContext map[string]any) (*v1.CheckBulkPermissionsRequestItem, error) {
	item := &v1.CheckBulkPermissionsRequestItem{
		Resource: &v1.ObjectReference{
			ObjectType: r.ResourceType,
			ObjectId:   r.ResourceID,
		},
		Permission: permission,
		Subject: &v1.SubjectReference{
			Object: &v1.ObjectReference{
				ObjectType: r.SubjectType,
				ObjectId:   r.SubjectID,
			},
			OptionalRelation: r.SubjectRelation,
		},
	}

	if merged := mergeCheckContext(callLevelContext, r.CheckContext); merged != nil {
		// Shares rel.CaveatContextToStruct with the write path
		// (rel.Relationship.ToProto) -- one converter for both surfaces, so
		// the two can never drift apart in what they accept or how they name
		// a failure. The error it returns wraps rel.ErrInvalidCaveatContext
		// and names the offending key.
		ctx, err := rel.CaveatContextToStruct(merged)
		if err != nil {
			return nil, &Error{
				Code:    CodeInvalidArgument,
				Message: fmt.Sprintf("spicedb: check %s/%s: %s", r.ResourceType, r.ResourceID, err),
				err:     err,
			}
		}
		item.Context = ctx
	}

	return item, nil
}
