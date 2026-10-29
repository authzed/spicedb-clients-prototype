package client

import (
	"context"
	"fmt"
	"io"
	"iter"

	"google.golang.org/grpc/status"

	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
	"github.com/authzed/spicedb-clients/spicedb-go/consistency"
	"github.com/authzed/spicedb-clients/spicedb-go/rel"
)

const (
	defaultReadPageSize   = 512
	defaultDeletePageSize = 1000
)

type WriteRequest struct{ Transaction rel.Txn }
type WriteResult struct{ Revision consistency.ZedToken }

func (c *Client) Write(ctx context.Context, request WriteRequest, opts ...WriteOption) (WriteResult, error) {
	o := operationOptions{}
	applyOptions(c, &o, opts)
	ctx, cancel, e := c.operation(ctx, o, false)
	if e != nil {
		return WriteResult{}, e
	}
	defer cancel()
	updates := request.Transaction.Updates()
	ps, e := preconditions(request.Transaction.Preconditions())
	if e != nil {
		return WriteResult{}, e
	}
	metadata, e := contextStruct(o.metadata)
	if e != nil {
		return WriteResult{}, e
	}
	req := &v1.WriteRelationshipsRequest{OptionalPreconditions: ps, OptionalTransactionMetadata: metadata}
	for _, u := range updates {
		r, e := relationship(u.Relationship)
		if e != nil {
			return WriteResult{}, e
		}
		var op v1.RelationshipUpdate_Operation
		switch u.Operation {
		case rel.UpdateOperationCreate:
			op = v1.RelationshipUpdate_OPERATION_CREATE
		case rel.UpdateOperationTouch:
			op = v1.RelationshipUpdate_OPERATION_TOUCH
		case rel.UpdateOperationDelete:
			op = v1.RelationshipUpdate_OPERATION_DELETE
		default:
			return WriteResult{}, invalid("unknown mutation operation")
		}
		req.Updates = append(req.Updates, &v1.RelationshipUpdate{Operation: op, Relationship: r})
	}
	// An empty transaction is sent as is: SpiceDB accepts it, evaluates any
	// preconditions, and returns the current revision.
	rs, e := mutationRetry(o, request.Transaction.IsIdempotent(), "write")
	if e != nil {
		return WriteResult{}, e
	}
	resp, e := retry(ctx, rs, "write", func() (*v1.WriteRelationshipsResponse, error) {
		return c.psc.WriteRelationships(ctx, req, o.callOptions...)
	})
	if e != nil {
		return WriteResult{}, e
	}
	return WriteResult{consistency.NewZedToken(resp.GetWrittenAt().GetToken())}, nil
}

// ReadRelationship is a relationship read from SpiceDB with its paging state.
// It embeds rel.Relationship, so relationship methods apply directly; pass
// r.Relationship where a rel.Relationship is required.
type ReadRelationship struct {
	rel.Relationship
	// AfterResultCursor resumes a read after this item, via
	// WithReadRelationshipsCursor.
	AfterResultCursor string
	// ReadAt is the revision of the response that carried this item.
	ReadAt consistency.ZedToken
}

type ReadRelationshipsRequest struct {
	Filter      rel.Filter
	Consistency consistency.Strategy
}

func (c *Client) ReadRelationships(ctx context.Context, request ReadRelationshipsRequest, opts ...ReadRelationshipsOption) iter.Seq2[ReadRelationship, error] {
	o := operationOptions{pageSize: defaultReadPageSize}
	applyOptions(c, &o, opts)
	f, fe := filter(request.Filter)
	initialConsistency, ce := strategy(request.Consistency)
	return func(yield func(ReadRelationship, error) bool) {
		cs := initialConsistency
		ctx, cancel, e := c.operation(ctx, o, true)
		if e != nil {
			yield(ReadRelationship{}, e)
			return
		}
		defer cancel()
		if fe != nil {
			yield(ReadRelationship{}, fe)
			return
		}
		if ce != nil {
			yield(ReadRelationship{}, ce)
			return
		}
		cur := cursor(o.cursor)
		var total uint64
		exposed := false
		// SpiceDB hashes the request, including its consistency and limit,
		// into each cursor and rejects a cursor sent with different arguments.
		// Every page therefore repeats the caller's consistency and one page
		// size; the cursor itself pins later pages to the first page's
		// revision, and the total limit is enforced here.
		pageSize := o.pageRequestLimit()
		for {
			req := &v1.ReadRelationshipsRequest{Consistency: cs, RelationshipFilter: f, OptionalLimit: pageSize, OptionalCursor: cur}
			pageCtx, pageCancel := c.pageContext(ctx)
			seq := receive(pageCtx, c.retrySettings(o), "read relationships", !exposed, func(ctx context.Context) (receiver[*v1.ReadRelationshipsResponse], error) {
				return c.psc.ReadRelationships(ctx, req, o.callOptions...)
			})
			count := uint32(0)
			previous := cur.GetToken()
			for resp, e := range seq {
				if e != nil {
					pageCancel()
					yield(ReadRelationship{}, e)
					return
				}
				count++
				total++
				cur = resp.GetAfterResultCursor()
				r, e := nativeRelationship("read relationships", resp.GetRelationship())
				if e != nil {
					pageCancel()
					yield(ReadRelationship{}, e)
					return
				}
				exposed = true
				item := ReadRelationship{Relationship: r, AfterResultCursor: cur.GetToken(), ReadAt: consistency.NewZedToken(resp.GetReadAt().GetToken())}
				if !yield(item, nil) {
					pageCancel()
					return
				}
				if o.limit > 0 && total >= o.limit {
					pageCancel()
					return
				}
			}
			pageCancel()
			if o.singleRequest || count < pageSize {
				return
			}
			if cur.GetToken() == "" || cur.GetToken() == previous {
				yield(ReadRelationship{}, &Error{Code: CodeInternal, Message: "spicedb: read page did not advance cursor"})
				return
			}
		}
	}
}

type DeleteRequest struct {
	Filter rel.Filter
	Limit  uint32
}
type DeleteAllRequest struct{ Filter rel.Filter }
type DeleteResult struct {
	Revision     consistency.ZedToken
	DeletedCount uint64
	Complete     bool
	Cursor       string
}

// DeleteProgressError carries only acknowledged pages. An interrupted request may have committed.
type DeleteProgressError struct {
	Progress DeleteResult
	Err      error
}

func (e *DeleteProgressError) Error() string {
	return fmt.Sprintf("spicedb: delete interrupted after %d acknowledged deletions: %v", e.Progress.DeletedCount, e.Err)
}
func (e *DeleteProgressError) Unwrap() error { return e.Err }

// GRPCStatus reports the underlying failure's status without the progress
// prefix Error adds.
func (e *DeleteProgressError) GRPCStatus() *status.Status { return statusOf(e.Err) }
func deleteResult(p *v1.DeleteRelationshipsResponse) DeleteResult {
	return DeleteResult{Revision: consistency.NewZedToken(p.GetDeletedAt().GetToken()), DeletedCount: p.GetRelationshipsDeletedCount(), Complete: p.GetDeletionProgress() == v1.DeleteRelationshipsResponse_DELETION_PROGRESS_COMPLETE, Cursor: p.GetAfterResultCursor().GetToken()}
}

// deletePage reports whether the server acknowledged a response, even when its progress enum is unknown.
func (c *Client) deletePage(ctx context.Context, f *v1.RelationshipFilter, limit uint32, o operationOptions) (DeleteResult, bool, error) {
	if e := ctx.Err(); e != nil {
		return DeleteResult{}, false, mapGRPCError("delete relationships", e)
	}
	ps, e := preconditions(o.preconditions)
	if e != nil {
		return DeleteResult{}, false, e
	}
	resp, e := c.psc.DeleteRelationships(ctx, &v1.DeleteRelationshipsRequest{RelationshipFilter: f, OptionalLimit: limit, OptionalAllowPartialDeletions: !o.disallowPartialDeletion, OptionalPreconditions: ps, OptionalCursor: cursor(o.cursor)}, o.callOptions...)
	if e != nil {
		return DeleteResult{}, false, mapGRPCError("delete relationships", e)
	}
	result := deleteResult(resp)
	if resp.GetDeletionProgress() != v1.DeleteRelationshipsResponse_DELETION_PROGRESS_COMPLETE && resp.GetDeletionProgress() != v1.DeleteRelationshipsResponse_DELETION_PROGRESS_PARTIAL {
		return result, true, &Error{Code: CodeInternal, Message: "spicedb: unknown delete progress; refusing another mutation"}
	}
	return result, true, nil
}

// DeleteRelationships issues exactly one bounded mutation; it never replays an ambiguous failure.
//
// By default it deletes up to Limit relationships (which must be positive) and
// reports whether it completed. With WithDeletePartial(PartialDisallowed) it is
// all-or-nothing: the server rejects a delete that would exceed the limit, and
// a zero Limit uses the server's configured default.
func (c *Client) DeleteRelationships(ctx context.Context, request DeleteRequest, opts ...DeleteOption) (DeleteResult, error) {
	o := operationOptions{}
	applyOptions(c, &o, opts)
	if _, e := mutationRetry(o, false, "delete relationships"); e != nil {
		return DeleteResult{}, e
	}
	ctx, cancel, e := c.operation(ctx, o, false)
	if e != nil {
		return DeleteResult{}, e
	}
	defer cancel()
	if request.Limit == 0 && !o.disallowPartialDeletion {
		return DeleteResult{}, invalid("delete total limit must be positive")
	}
	f, e := filter(request.Filter)
	if e != nil {
		return DeleteResult{}, e
	}
	result, _, err := c.deletePage(ctx, f, request.Limit, o)
	return result, err
}

// DeleteAllRelationshipsNonAtomic deletes in transactional batches, but the whole operation is not atomic.
// Completed batches remain committed if a later batch fails. Preconditions are re-evaluated on every page.
// Concurrent writes and ambiguous page failures can prevent exact recovery.
func (c *Client) DeleteAllRelationshipsNonAtomic(ctx context.Context, request DeleteAllRequest, opts ...DeleteAllOption) (DeleteResult, error) {
	o := operationOptions{pageSize: defaultDeletePageSize}
	applyOptions(c, &o, opts)
	if _, e := mutationRetry(o, false, "delete relationships"); e != nil {
		return DeleteResult{}, e
	}
	ctx, cancel, e := c.operation(ctx, o, false)
	if e != nil {
		return DeleteResult{}, e
	}
	defer cancel()
	f, e := filter(request.Filter)
	if e != nil {
		return DeleteResult{}, e
	}
	progress := DeleteResult{}
	for {
		page, acknowledged, e := c.deletePage(ctx, f, o.pageSize, o)
		if e != nil {
			if acknowledged {
				progress.Revision = page.Revision
				progress.DeletedCount += page.DeletedCount
				progress.Cursor = page.Cursor
			}
			return progress, &DeleteProgressError{progress, e}
		}
		progress.Revision = page.Revision
		progress.DeletedCount += page.DeletedCount
		progress.Complete = page.Complete
		progress.Cursor = page.Cursor
		if o.deleteProgress != nil {
			o.deleteProgress(progress)
		}
		if page.Complete {
			return progress, nil
		}
		if page.DeletedCount == 0 {
			return progress, &DeleteProgressError{progress, &Error{Code: CodeInternal, Message: "spicedb: partial delete made no progress"}}
		}
		o.cursor = page.Cursor
	}
}

// receiver is the transport receive surface used by all server streams.
type receiver[T any] interface{ Recv() (T, error) }

// receive retries establishment and the first receive only before any item is exposed.
func receive[T any](ctx context.Context, rs retrySettings, op string, mayRetry bool, open func(context.Context) (receiver[T], error)) iter.Seq2[T, error] {
	policy := rs.policy
	if !rs.streams {
		mayRetry = false
	}
	return func(yield func(T, error) bool) {
		var zero T
		attempt := 0
		for {
			if e := ctx.Err(); e != nil {
				yield(zero, mapGRPCError(op, e))
				return
			}
			attemptCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			stream, e := open(attemptCtx)
			var first T
			if e == nil {
				first, e = stream.Recv()
			}
			if e != nil {
				cancel()
				if e == io.EOF {
					return
				}
				if !mayRetry || attempt >= policy.MaxRetries || !policy.retryable(e) {
					yield(zero, mapGRPCError(op, e))
					return
				}
				if e := policy.backoff(ctx, op, attempt, e); e != nil {
					yield(zero, mapGRPCError(op, e))
					return
				}
				attempt++
				continue
			}
			if !yield(first, nil) {
				cancel()
				return
			}
			for {
				item, e := stream.Recv()
				if e != nil {
					cancel()
					if e != io.EOF {
						yield(zero, mapGRPCError(op, e))
					}
					return
				}
				if !yield(item, nil) {
					cancel()
					return
				}
			}
		}
	}
}
