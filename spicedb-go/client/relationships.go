package client

import (
	"context"
	"fmt"
	"io"
	"iter"

	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
	"github.com/authzed/spicedb-clients/spicedb-go/consistency"
	"github.com/authzed/spicedb-clients/spicedb-go/rel"
)

const (
	defaultReadPageSize   = 512
	defaultDeletePageSize = 1_000
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
		filterProto, err := f.ToProto()
		if err != nil {
			yield(rel.Relationship{}, &Error{
				Code:    CodeInvalidArgument,
				Message: fmt.Sprintf("spicedb: read relationships: %s", err),
				err:     err,
			})
			return
		}

		var cursor *v1.Cursor
		for {
			stream, err := c.psc.ReadRelationships(ctx, &v1.ReadRelationshipsRequest{
				Consistency:        cs.V1Consistency,
				RelationshipFilter: filterProto,
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

// DeleteOption configures an optional aspect of a DeleteRelationships call:
// preconditions that guard the delete, or an override of the per-call page
// size.
type DeleteOption func(*deleteOptions)

type deleteOptions struct {
	preconditions []*v1.Precondition
	limit         uint32 // 0 = use defaultDeletePageSize
	err           error  // first Filter.ToProto conversion error, if any, encountered while applying options
}

// WithDeleteMustMatch adds a MUST_MATCH precondition to the delete: the
// server rejects the delete (and returns an error, deleting nothing) unless
// at least one relationship matching filter exists at evaluation time.
// Multiple precondition options accumulate; all are sent with every
// request. If filter cannot be converted to protobuf (see Filter.ToProto),
// the conversion error is deferred and surfaced by DeleteRelationships --
// DeleteOption's signature can't return an error directly.
func WithDeleteMustMatch(filter rel.Filter) DeleteOption {
	return func(o *deleteOptions) {
		p, err := filter.ToProto()
		if err != nil {
			if o.err == nil {
				o.err = err
			}
			return
		}
		o.preconditions = append(o.preconditions, &v1.Precondition{
			Operation: v1.Precondition_OPERATION_MUST_MATCH,
			Filter:    p,
		})
	}
}

// WithDeleteMustNotMatch adds a MUST_NOT_MATCH precondition to the delete:
// the server rejects the delete (and returns an error, deleting nothing) if
// any relationship matching filter exists at evaluation time. Multiple
// precondition options accumulate; all are sent with every request. If
// filter cannot be converted to protobuf (see Filter.ToProto), the
// conversion error is deferred and surfaced by DeleteRelationships --
// DeleteOption's signature can't return an error directly.
func WithDeleteMustNotMatch(filter rel.Filter) DeleteOption {
	return func(o *deleteOptions) {
		p, err := filter.ToProto()
		if err != nil {
			if o.err == nil {
				o.err = err
			}
			return
		}
		o.preconditions = append(o.preconditions, &v1.Precondition{
			Operation: v1.Precondition_OPERATION_MUST_NOT_MATCH,
			Filter:    p,
		})
	}
}

// WithDeleteLimit overrides the default per-request page size (1,000) used
// by DeleteRelationships' auto-paging loop.
func WithDeleteLimit(n uint32) DeleteOption {
	return func(o *deleteOptions) {
		o.limit = n
	}
}

// DeleteRelationships deletes all relationships matching the given filter.
// Large result sets are automatically paged in batches of 1,000 (override
// with WithDeleteLimit). Returns the revision of the final deletion.
//
// WithDeleteMustMatch/WithDeleteMustNotMatch add preconditions that guard
// the delete: if a precondition fails, the server rejects that call and
// deletes nothing for it. Preconditions are a per-request proto field, so
// when a delete spans multiple pages (i.e. more matches than the limit),
// they are re-evaluated by the server on every page — there is no
// "check-once, apply-to-all-pages" semantics. This means a delete that
// starts successfully can still fail partway through if the guarded state
// changes between pages, after earlier pages have already been deleted. For
// a single-shot, all-or-nothing guarded delete, pair the precondition with a
// WithDeleteLimit large enough to cover every matching relationship in one
// call.
func (c *Client) DeleteRelationships(ctx context.Context, f rel.Filter, opts ...DeleteOption) (revision string, err error) {
	var o deleteOptions
	for _, opt := range opts {
		opt(&o)
	}
	if o.err != nil {
		return "", &Error{
			Code:    CodeInvalidArgument,
			Message: fmt.Sprintf("spicedb: delete relationships: %s", o.err),
			err:     o.err,
		}
	}

	filterProto, err := f.ToProto()
	if err != nil {
		return "", &Error{
			Code:    CodeInvalidArgument,
			Message: fmt.Sprintf("spicedb: delete relationships: %s", err),
			err:     err,
		}
	}

	limit := uint32(defaultDeletePageSize)
	if o.limit != 0 {
		limit = o.limit
	}

	for {
		resp, err := c.psc.DeleteRelationships(ctx, &v1.DeleteRelationshipsRequest{
			RelationshipFilter:            filterProto,
			OptionalPreconditions:         o.preconditions,
			OptionalLimit:                 limit,
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
