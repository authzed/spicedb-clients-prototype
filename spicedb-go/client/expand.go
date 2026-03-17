package client

import (
	"context"
	"fmt"

	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
	"github.com/authzed/spicedb-clients/spicedb-go/consistency"
)

// ExpandResult holds the result of a permission tree expansion.
type ExpandResult struct {
	// TreeRoot is the root of the expanded permission tree. This is the
	// underlying proto type since the tree structure is complex and deeply
	// nested.
	TreeRoot *v1.PermissionRelationshipTree
	Revision string
}

// ExpandPermissionTree expands the permission tree for the given resource and
// permission, returning the full tree of subjects with access.
func (c *Client) ExpandPermissionTree(ctx context.Context, cs consistency.Strategy, resourceType, resourceID, permission string) (*ExpandResult, error) {
	resp, err := c.psc.ExpandPermissionTree(ctx, &v1.ExpandPermissionTreeRequest{
		Consistency: cs.V1Consistency,
		Resource: &v1.ObjectReference{
			ObjectType: resourceType,
			ObjectId:   resourceID,
		},
		Permission: permission,
	})
	if err != nil {
		return nil, fmt.Errorf("spicedb: expand permission tree: %w", err)
	}

	return &ExpandResult{
		TreeRoot: resp.GetTreeRoot(),
		Revision: resp.GetExpandedAt().GetToken(),
	}, nil
}
