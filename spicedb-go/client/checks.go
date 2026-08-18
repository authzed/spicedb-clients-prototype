package client

import (
	"context"
	"fmt"
	"iter"

	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"

	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
	"github.com/authzed/spicedb-clients/spicedb-go/consistency"
	"github.com/authzed/spicedb-clients/spicedb-go/rel"
)

const defaultCheckBatchSize = 1000

// Check performs a bulk permission check on the given relationships and
// returns a CheckResult for each relationship. All checks use
// BulkCheckPermissions under the hood.
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
	if len(rs) == 0 {
		return nil, nil
	}

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

	checkedAt := resp.GetCheckedAt().GetToken()
	results := make([]CheckResult, len(resp.GetPairs()))
	for i, pair := range resp.GetPairs() {
		if errResp := pair.GetError(); errResp != nil {
			return nil, mapGRPCError(fmt.Sprintf("check item %d", i), status.FromProto(errResp).Err())
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
			if err != nil {
				return yield(CheckResult{}, err)
			}
			for _, result := range results {
				if !yield(result, nil) {
					return false
				}
			}
			batch = batch[:0]
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
// protobuf Struct (structpb.NewStruct fails on values it cannot represent,
// e.g. unsupported types), so a caller never mistakes "your context was
// dropped" for "the server needed more context than you supplied".
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
		ctx, err := structpb.NewStruct(merged)
		if err != nil {
			return nil, &Error{
				Code:    CodeInvalidArgument,
				Message: fmt.Sprintf("spicedb: check %s/%s: invalid caveat context: %s", r.ResourceType, r.ResourceID, err),
				err:     err,
			}
		}
		item.Context = ctx
	}

	return item, nil
}
