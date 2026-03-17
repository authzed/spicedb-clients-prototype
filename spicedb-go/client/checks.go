package client

import (
	"context"
	"fmt"
	"iter"

	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
	"github.com/authzed/spicedb-clients/spicedb-go/consistency"
	"github.com/authzed/spicedb-clients/spicedb-go/rel"
)

const defaultCheckBatchSize = 1000

// Check performs a bulk permission check on the given relationships and returns
// a bool for each relationship indicating whether permission is granted.
// All checks use BulkCheckPermissions under the hood.
func (c *Client) Check(ctx context.Context, cs consistency.Strategy, permission string, rs ...rel.Relationship) ([]bool, error) {
	if len(rs) == 0 {
		return nil, nil
	}

	items := make([]*v1.CheckBulkPermissionsRequestItem, len(rs))
	for i, r := range rs {
		items[i] = checkItemFromRel(r, permission)
	}

	resp, err := c.psc.CheckBulkPermissions(ctx, &v1.CheckBulkPermissionsRequest{
		Consistency: cs.V1Consistency,
		Items:       items,
	})
	if err != nil {
		return nil, fmt.Errorf("spicedb: check: %w", err)
	}

	results := make([]bool, len(resp.GetPairs()))
	for i, pair := range resp.GetPairs() {
		if errResp := pair.GetError(); errResp != nil {
			return nil, fmt.Errorf("spicedb: check item %d: %s", i, errResp.GetMessage())
		}
		item := pair.GetItem()
		results[i] = item.GetPermissionship() == v1.CheckPermissionResponse_PERMISSIONSHIP_HAS_PERMISSION
	}
	return results, nil
}

// CheckOne checks a single permission and returns true if granted.
func (c *Client) CheckOne(ctx context.Context, cs consistency.Strategy, permission string, r rel.Relationship) (bool, error) {
	results, err := c.Check(ctx, cs, permission, r)
	if err != nil {
		return false, err
	}
	return results[0], nil
}

// CheckAny returns true if any of the given relationships have the permission.
func (c *Client) CheckAny(ctx context.Context, cs consistency.Strategy, permission string, rs ...rel.Relationship) (bool, error) {
	results, err := c.Check(ctx, cs, permission, rs...)
	if err != nil {
		return false, err
	}
	for _, r := range results {
		if r {
			return true, nil
		}
	}
	return false, nil
}

// CheckAll returns true if all of the given relationships have the permission.
func (c *Client) CheckAll(ctx context.Context, cs consistency.Strategy, permission string, rs ...rel.Relationship) (bool, error) {
	results, err := c.Check(ctx, cs, permission, rs...)
	if err != nil {
		return false, err
	}
	for _, r := range results {
		if !r {
			return false, nil
		}
	}
	return true, nil
}

// CheckIter checks permissions for relationships from an iterator, yielding
// results as they are computed. Relationships are automatically batched into
// chunks of 1,000 for efficient bulk checking.
func (c *Client) CheckIter(ctx context.Context, cs consistency.Strategy, permission string, rels iter.Seq[rel.Relationship]) iter.Seq2[bool, error] {
	return func(yield func(bool, error) bool) {
		batch := make([]rel.Relationship, 0, defaultCheckBatchSize)

		flush := func() bool {
			if len(batch) == 0 {
				return true
			}
			results, err := c.Check(ctx, cs, permission, batch...)
			if err != nil {
				return yield(false, err)
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

func checkItemFromRel(r rel.Relationship, permission string) *v1.CheckBulkPermissionsRequestItem {
	return &v1.CheckBulkPermissionsRequestItem{
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
}
