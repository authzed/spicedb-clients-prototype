package client

import (
	"context"
	"fmt"

	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
	"github.com/authzed/spicedb-clients/spicedb-go/rel"
)

// CountResult holds the result of a relationship count operation.
type CountResult struct {
	RelationshipCount uint64
	Revision          string
}

// Experimental: this API wraps SpiceDB's ExperimentalService and may change without notice.
// RegisterRelationshipCounter registers a named counter that tracks
// relationships matching the given filter. The counter is computed
// asynchronously by SpiceDB.
func (c *Client) RegisterRelationshipCounter(ctx context.Context, name string, f rel.Filter) error {
	filterProto, err := f.ToProto()
	if err != nil {
		return &Error{
			Code:    CodeInvalidArgument,
			Message: fmt.Sprintf("spicedb: register relationship counter: %s", err),
			err:     err,
		}
	}
	_, err = c.esc.ExperimentalRegisterRelationshipCounter(ctx, &v1.ExperimentalRegisterRelationshipCounterRequest{
		Name:               name,
		RelationshipFilter: filterProto,
	})
	if err != nil {
		return mapGRPCError("register relationship counter", err)
	}
	return nil
}

// Experimental: this API wraps SpiceDB's ExperimentalService and may change without notice.
// CountRelationships reads the value of a previously registered relationship
// counter. Returns the count and revision, or a boolean indicating the counter
// is still being calculated (in which case count and revision are zero values).
func (c *Client) CountRelationships(ctx context.Context, name string) (result *CountResult, stillCalculating bool, err error) {
	resp, err := c.esc.ExperimentalCountRelationships(ctx, &v1.ExperimentalCountRelationshipsRequest{
		Name: name,
	})
	if err != nil {
		return nil, false, mapGRPCError("count relationships", err)
	}

	if resp.GetCounterStillCalculating() {
		return nil, true, nil
	}

	cv := resp.GetReadCounterValue()
	return &CountResult{
		RelationshipCount: cv.GetRelationshipCount(),
		Revision:          cv.GetReadAt().GetToken(),
	}, false, nil
}

// Experimental: this API wraps SpiceDB's ExperimentalService and may change without notice.
// UnregisterRelationshipCounter removes a previously registered relationship
// counter.
func (c *Client) UnregisterRelationshipCounter(ctx context.Context, name string) error {
	_, err := c.esc.ExperimentalUnregisterRelationshipCounter(ctx, &v1.ExperimentalUnregisterRelationshipCounterRequest{
		Name: name,
	})
	if err != nil {
		return mapGRPCError("unregister relationship counter", err)
	}
	return nil
}
