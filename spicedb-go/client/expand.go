package client

import (
	"context"

	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
	"github.com/authzed/spicedb-clients/spicedb-go/consistency"
)

// ExpandResult holds the result of a permission tree expansion.
type ExpandResult struct {
	// Tree is the root of the expanded permission tree, as a native
	// PermissionTree (see expand_tree.go).
	Tree     PermissionTree
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
		return nil, mapGRPCError("expand permission tree", err)
	}

	return &ExpandResult{
		Tree:     toPermissionTree(resp.GetTreeRoot()),
		Revision: resp.GetExpandedAt().GetToken(),
	}, nil
}
