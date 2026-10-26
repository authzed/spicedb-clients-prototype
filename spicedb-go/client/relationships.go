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
	defaultDeletePageSize = 1000
)

type WriteRequest struct{ Transaction rel.Txn }
type WriteResult struct{ Revision consistency.ZedToken }

func (c *Client) Write(ctx context.Context, request WriteRequest, opts ...WriteOption) (WriteResult, error) {
	o := operationOptions{}
	for _, opt := range opts {
		opt(&o)
	}
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
	if len(req.Updates) == 0 {
		return WriteResult{}, invalid("transaction requires updates")
	}
	resp, e := c.psc.WriteRelationships(ctx, req)
	if e != nil {
		return WriteResult{}, mapGRPCError("write", e)
	}
	return WriteResult{consistency.NewZedToken(resp.GetWrittenAt().GetToken())}, nil
}

type ReadRelationshipsRequest struct {
	Filter      rel.Filter
	Consistency consistency.Strategy
}

func (c *Client) ReadRelationships(ctx context.Context, request ReadRelationshipsRequest, opts ...ReadRelationshipsOption) iter.Seq2[rel.Relationship, error] {
	o := operationOptions{pageSize: defaultReadPageSize}
	for _, opt := range opts {
		opt(&o)
	}
	f, fe := filter(request.Filter)
	initialConsistency, ce := strategy(request.Consistency)
	return func(yield func(rel.Relationship, error) bool) {
		cs := initialConsistency
		ctx, cancel, e := c.operation(ctx, o, true)
		if e != nil {
			yield(rel.Relationship{}, e)
			return
		}
		defer cancel()
		if fe != nil {
			yield(rel.Relationship{}, fe)
			return
		}
		if ce != nil {
			yield(rel.Relationship{}, ce)
			return
		}
		var cur *v1.Cursor
		var total uint64
		exposed := false
		for {
			pageSize := o.pageSize
			if o.limit > 0 {
				pageSize = uint32(min(uint64(pageSize), o.limit-total))
			}
			req := &v1.ReadRelationshipsRequest{Consistency: cs, RelationshipFilter: f, OptionalLimit: pageSize, OptionalCursor: cur}
			pageCtx, pageCancel := context.WithCancel(ctx)
			seq := receive(pageCtx, "read relationships", !exposed, func(ctx context.Context) (receiver[*v1.ReadRelationshipsResponse], error) {
				return c.psc.ReadRelationships(ctx, req)
			})
			count := uint32(0)
			previous := cur.GetToken()
			for resp, e := range seq {
				if e != nil {
					pageCancel()
					yield(rel.Relationship{}, e)
					return
				}
				count++
				total++
				cur = resp.GetAfterResultCursor()
				if !exposed && resp.GetReadAt().GetToken() != "" {
					cs, _ = strategy(consistency.Snapshot(consistency.NewZedToken(resp.GetReadAt().GetToken())))
				}
				r, e := nativeRelationship(resp.GetRelationship())
				if e != nil {
					pageCancel()
					yield(rel.Relationship{}, e)
					return
				}
				exposed = true
				if !yield(r, nil) {
					pageCancel()
					return
				}
				if o.limit > 0 && total >= o.limit {
					pageCancel()
					return
				}
			}
			pageCancel()
			if count < pageSize {
				return
			}
			if cur.GetToken() == "" || cur.GetToken() == previous {
				yield(rel.Relationship{}, &Error{Code: CodeInternal, Message: "spicedb: read page did not advance cursor"})
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
	resp, e := c.psc.DeleteRelationships(ctx, &v1.DeleteRelationshipsRequest{RelationshipFilter: f, OptionalLimit: limit, OptionalAllowPartialDeletions: true, OptionalPreconditions: ps, OptionalCursor: cursor(o.cursor)})
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
func (c *Client) DeleteRelationships(ctx context.Context, request DeleteRequest, opts ...DeleteOption) (DeleteResult, error) {
	o := operationOptions{}
	for _, opt := range opts {
		opt(&o)
	}
	ctx, cancel, e := c.operation(ctx, o, false)
	if e != nil {
		return DeleteResult{}, e
	}
	defer cancel()
	if request.Limit == 0 {
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
	for _, opt := range opts {
		opt(&o)
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
func receive[T any](ctx context.Context, op string, mayRetry bool, open func(context.Context) (receiver[T], error)) iter.Seq2[T, error] {
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
				if !mayRetry || attempt >= 3 || !transient(e) {
					yield(zero, mapGRPCError(op, e))
					return
				}
				if e := backoff(ctx, attempt); e != nil {
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
