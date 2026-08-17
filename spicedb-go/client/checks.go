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

// CheckOption configures an optional aspect of a check call: currently, a
// call-level default caveat context applied to every relationship checked.
type CheckOption func(*checkOptions)

type checkOptions struct {
	context map[string]any
}

// WithCheckContext sets a default caveat context applied to every
// relationship checked in the call. Caveat context supplies named values
// (e.g. "now") that SpiceDB needs to evaluate a caveat expression
// encountered during the check; without it, a caveated match comes back as
// CheckResult.Permissionship == PermissionshipConditionalPermission instead
// of a grant, and CheckResult.MissingContext names what was needed.
//
// A relationship built with rel.Relationship.WithCheckContext overrides this
// default on a per-key basis for that one relationship: the item's keys win
// on conflict, and any call-level keys the item doesn't specify are
// retained. For example, a call-level {"now": 42, "region": "us"} plus a
// per-item {"region": "eu"} sends {"now": 42, "region": "eu"} for that item,
// while a sibling item with no per-item context still gets the untouched
// call-level default.
func WithCheckContext(context map[string]any) CheckOption {
	return func(o *checkOptions) {
		o.context = context
	}
}

// Check performs a bulk permission check on the given relationships and
// returns a CheckResult for each relationship. All checks use
// BulkCheckPermissions under the hood.
//
// CheckResult.Permissionship carries the server's three-valued answer — a
// Conditional result means the server needed caveat context that was not
// supplied, and is NOT a grant. Prefer CheckResult.HasPermission() over
// comparing Permissionship directly for the common case.
//
// WithCheckContext supplies a call-level default caveat context; per-item
// context set via rel.Relationship.WithCheckContext merges with it (see
// WithCheckContext for the merge rule).
func (c *Client) Check(ctx context.Context, cs consistency.Strategy, permission string, rs []rel.Relationship, opts ...CheckOption) ([]CheckResult, error) {
	if len(rs) == 0 {
		return nil, nil
	}

	var o checkOptions
	for _, opt := range opts {
		opt(&o)
	}

	items := make([]*v1.CheckBulkPermissionsRequestItem, len(rs))
	for i, r := range rs {
		items[i] = checkItemFromRel(r, permission, o.context)
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
func (c *Client) CheckOne(ctx context.Context, cs consistency.Strategy, permission string, r rel.Relationship, opts ...CheckOption) (CheckResult, error) {
	results, err := c.Check(ctx, cs, permission, []rel.Relationship{r}, opts...)
	if err != nil {
		return CheckResult{}, err
	}
	return results[0], nil
}

// CheckAny returns true if any of the given relationships have the
// permission outright. A Conditional result does not count as granted — only
// CheckResult.HasPermission() results are considered.
func (c *Client) CheckAny(ctx context.Context, cs consistency.Strategy, permission string, rs []rel.Relationship, opts ...CheckOption) (bool, error) {
	results, err := c.Check(ctx, cs, permission, rs, opts...)
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
func (c *Client) CheckAll(ctx context.Context, cs consistency.Strategy, permission string, rs []rel.Relationship, opts ...CheckOption) (bool, error) {
	results, err := c.Check(ctx, cs, permission, rs, opts...)
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
func (c *Client) CheckIter(ctx context.Context, cs consistency.Strategy, permission string, rels iter.Seq[rel.Relationship], opts ...CheckOption) iter.Seq2[CheckResult, error] {
	return func(yield func(CheckResult, error) bool) {
		batch := make([]rel.Relationship, 0, defaultCheckBatchSize)

		flush := func() bool {
			if len(batch) == 0 {
				return true
			}
			results, err := c.Check(ctx, cs, permission, batch, opts...)
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

func checkItemFromRel(r rel.Relationship, permission string, callLevelContext map[string]any) *v1.CheckBulkPermissionsRequestItem {
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
		if ctx, err := structpb.NewStruct(merged); err == nil {
			item.Context = ctx
		}
	}

	return item
}
