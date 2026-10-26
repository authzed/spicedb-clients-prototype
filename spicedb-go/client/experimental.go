package client

import (
	"context"

	materialize "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/materialize/v0"
	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
	"github.com/authzed/spicedb-clients/spicedb-go/consistency"
	"github.com/authzed/spicedb-clients/spicedb-go/rel"
)

type RegisterRelationshipCounterRequest struct {
	Name   string
	Filter rel.Filter
}
type RegisterRelationshipCounterResult struct{}
type CountRelationshipsRequest struct{ Name string }
type CountRelationshipsResult struct {
	RelationshipCount uint64
	Revision          consistency.ZedToken
	StillCalculating  bool
}
type UnregisterRelationshipCounterRequest struct{ Name string }
type UnregisterRelationshipCounterResult struct{}

// Experimental: RegisterRelationshipCounter registers a mutable asynchronous counter.
func (c *Client) RegisterRelationshipCounter(ctx context.Context, request RegisterRelationshipCounterRequest, opts ...RegisterRelationshipCounterOption) (RegisterRelationshipCounterResult, error) {
	o := operationOptions{}
	for _, opt := range opts {
		opt(&o)
	}
	ctx, cancel, e := c.operation(ctx, o, false)
	if e != nil {
		return RegisterRelationshipCounterResult{}, e
	}
	defer cancel()
	if request.Name == "" {
		return RegisterRelationshipCounterResult{}, invalid("counter name required")
	}
	f, e := filter(request.Filter)
	if e != nil {
		return RegisterRelationshipCounterResult{}, e
	}
	_, e = c.esc.ExperimentalRegisterRelationshipCounter(ctx, &v1.ExperimentalRegisterRelationshipCounterRequest{Name: request.Name, RelationshipFilter: f})
	return RegisterRelationshipCounterResult{}, mapGRPCError("register relationship counter", e)
}

// Experimental: CountRelationships reads a registered asynchronous counter.
func (c *Client) CountRelationships(ctx context.Context, request CountRelationshipsRequest, opts ...CountRelationshipsOption) (CountRelationshipsResult, error) {
	o := operationOptions{}
	for _, opt := range opts {
		opt(&o)
	}
	ctx, cancel, e := c.operation(ctx, o, false)
	if e != nil {
		return CountRelationshipsResult{}, e
	}
	defer cancel()
	if request.Name == "" {
		return CountRelationshipsResult{}, invalid("counter name required")
	}
	resp, e := retry(ctx, "count relationships", func() (*v1.ExperimentalCountRelationshipsResponse, error) {
		return c.esc.ExperimentalCountRelationships(ctx, &v1.ExperimentalCountRelationshipsRequest{Name: request.Name})
	})
	if e != nil {
		return CountRelationshipsResult{}, e
	}
	if resp.GetCounterResult() == nil {
		return CountRelationshipsResult{}, &Error{Code: CodeInternal, Message: "spicedb: counter response has no state"}
	}
	return CountRelationshipsResult{RelationshipCount: resp.GetReadCounterValue().GetRelationshipCount(), Revision: consistency.NewZedToken(resp.GetReadCounterValue().GetReadAt().GetToken()), StillCalculating: resp.GetCounterStillCalculating()}, nil
}

// Experimental: UnregisterRelationshipCounter removes an asynchronous counter.
func (c *Client) UnregisterRelationshipCounter(ctx context.Context, request UnregisterRelationshipCounterRequest, opts ...UnregisterRelationshipCounterOption) (UnregisterRelationshipCounterResult, error) {
	o := operationOptions{}
	for _, opt := range opts {
		opt(&o)
	}
	ctx, cancel, e := c.operation(ctx, o, false)
	if e != nil {
		return UnregisterRelationshipCounterResult{}, e
	}
	defer cancel()
	if request.Name == "" {
		return UnregisterRelationshipCounterResult{}, invalid("counter name required")
	}
	_, e = c.esc.ExperimentalUnregisterRelationshipCounter(ctx, &v1.ExperimentalUnregisterRelationshipCounterRequest{Name: request.Name})
	return UnregisterRelationshipCounterResult{}, mapGRPCError("unregister relationship counter", e)
}

type RoaringLookupResourcesRequest struct {
	ResourceType, Permission string
	Subject                  rel.SubjectRef
	Consistency              consistency.Strategy
}
type RoaringLookupResourcesResult struct {
	Bitmap      []byte
	Cardinality uint64
	Revision    consistency.ZedToken
}

// Experimental: RoaringLookupResources calls materialize on the same authenticated connection.
func (c *Client) RoaringLookupResources(ctx context.Context, request RoaringLookupResourcesRequest, opts ...RoaringLookupResourcesOption) (RoaringLookupResourcesResult, error) {
	o := operationOptions{}
	for _, opt := range opts {
		opt(&o)
	}
	ctx, cancel, e := c.operation(ctx, o, false)
	if e != nil {
		return RoaringLookupResourcesResult{}, e
	}
	defer cancel()
	cs, e := strategy(request.Consistency)
	if e != nil {
		return RoaringLookupResourcesResult{}, e
	}
	if request.ResourceType == "" || request.Permission == "" || request.Subject.Validate() != nil {
		return RoaringLookupResourcesResult{}, invalid("resource type, permission and subject required")
	}
	if c.proto == nil || c.proto.Connection() == nil {
		return RoaringLookupResourcesResult{}, invalid("client has no connection")
	}
	stub := materialize.NewRoaringLookupResourcesServiceClient(c.proto.Connection())
	resp, e := retry(ctx, "roaring lookup resources", func() (*materialize.ExperimentalRoaringLookupResourcesResponse, error) {
		return stub.ExperimentalRoaringLookupResources(ctx, &materialize.ExperimentalRoaringLookupResourcesRequest{Consistency: cs, ResourceObjectType: request.ResourceType, Permission: request.Permission, Subject: subject(request.Subject)})
	})
	if e != nil {
		return RoaringLookupResourcesResult{}, e
	}
	return RoaringLookupResourcesResult{Bitmap: append([]byte(nil), resp.GetBitmap()...), Cardinality: resp.GetCardinality(), Revision: consistency.NewZedToken(resp.GetAtRevision().GetToken())}, nil
}
