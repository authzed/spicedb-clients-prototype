package client

import (
	"context"

	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
	"github.com/authzed/spicedb-clients/spicedb-go/consistency"
	"github.com/authzed/spicedb-clients/spicedb-go/rel"
)

type ExpandPermissionTreeRequest struct {
	Resource    rel.ObjectRef
	Permission  string
	Consistency consistency.Strategy
}
type ExpandPermissionTreeResult struct {
	Tree     PermissionTree
	Revision consistency.ZedToken
}

func (c *Client) ExpandPermissionTree(ctx context.Context, request ExpandPermissionTreeRequest, opts ...ExpandPermissionTreeOption) (ExpandPermissionTreeResult, error) {
	o := operationOptions{}
	for _, opt := range opts {
		opt(&o)
	}
	ctx, cancel, e := c.operation(ctx, o, false)
	if e != nil {
		return ExpandPermissionTreeResult{}, e
	}
	defer cancel()
	cs, e := strategy(request.Consistency)
	if e != nil {
		return ExpandPermissionTreeResult{}, e
	}
	if e := request.Resource.Validate(); e != nil {
		return ExpandPermissionTreeResult{}, invalid("resource: %s", e)
	}
	if request.Permission == "" {
		return ExpandPermissionTreeResult{}, invalid("permission required")
	}
	resp, e := retry(ctx, c.retrySettings(o), "expand permission tree", func() (*v1.ExpandPermissionTreeResponse, error) {
		return c.psc.ExpandPermissionTree(ctx, &v1.ExpandPermissionTreeRequest{Consistency: cs, Resource: object(request.Resource), Permission: request.Permission}, o.callOptions...)
	})
	if e != nil {
		return ExpandPermissionTreeResult{}, e
	}
	return ExpandPermissionTreeResult{toPermissionTree(resp.GetTreeRoot()), consistency.NewZedToken(resp.GetExpandedAt().GetToken())}, nil
}
