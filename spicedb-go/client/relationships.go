package client

import (
	"context"
	"io"
	"iter"

	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
	"github.com/authzed/spicedb-clients/spicedb-go/consistency"
	"github.com/authzed/spicedb-clients/spicedb-go/rel"
)

const (
	defaultReadPageSize   = 512
	defaultDeletePageSize = 10_000
)

// Write commits a transaction of relationship mutations to SpiceDB, returning
// the revision at which the write occurred.
func (c *Client) Write(ctx context.Context, txn rel.Txn) (revision string, err error) {
	req := &v1.WriteRelationshipsRequest{
		Updates: txn.V1Updates,
	}
	if len(txn.Preconditions()) > 0 {
		req.OptionalPreconditions = txn.Preconditions()
	}

	resp, err := c.psc.WriteRelationships(ctx, req)
	if err != nil {
		return "", mapGRPCError("write", err)
	}
	return resp.GetWrittenAt().GetToken(), nil
}

// ReadRelationships returns an iterator over relationships matching the given
// filter. Cursors are handled transparently — the client automatically
// re-fetches pages of 512 relationships using the AfterResultCursor.
func (c *Client) ReadRelationships(ctx context.Context, cs consistency.Strategy, f rel.Filter) iter.Seq2[rel.Relationship, error] {
	return func(yield func(rel.Relationship, error) bool) {
		var cursor *v1.Cursor
		for {
			stream, err := c.psc.ReadRelationships(ctx, &v1.ReadRelationshipsRequest{
				Consistency:        cs.V1Consistency,
				RelationshipFilter: f.ToProto(),
				OptionalLimit:      defaultReadPageSize,
				OptionalCursor:     cursor,
			})
			if err != nil {
				yield(rel.Relationship{}, mapGRPCError("read relationships", err))
				return
			}

			var count uint32
			for {
				resp, err := stream.Recv()
				if err == io.EOF {
					break
				}
				if err != nil {
					yield(rel.Relationship{}, mapGRPCError("read relationships", err))
					return
				}
				count++
				cursor = resp.GetAfterResultCursor()
				if !yield(rel.FromProto(resp.GetRelationship()), nil) {
					return
				}
			}

			// If we got fewer than the page size, we've read everything.
			if count < defaultReadPageSize {
				return
			}
		}
	}
}

// DeleteRelationships deletes all relationships matching the given filter.
// Large result sets are automatically paged in batches of 10,000. Returns
// the revision of the final deletion.
func (c *Client) DeleteRelationships(ctx context.Context, f rel.Filter) (revision string, err error) {
	for {
		resp, err := c.psc.DeleteRelationships(ctx, &v1.DeleteRelationshipsRequest{
			RelationshipFilter:          f.ToProto(),
			OptionalLimit:               defaultDeletePageSize,
			OptionalAllowPartialDeletions: true,
		})
		if err != nil {
			return "", mapGRPCError("delete relationships", err)
		}

		revision = resp.GetDeletedAt().GetToken()

		if resp.GetDeletionProgress() == v1.DeleteRelationshipsResponse_DELETION_PROGRESS_COMPLETE {
			return revision, nil
		}
	}
}
