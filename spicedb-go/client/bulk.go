package client

import (
	"context"
	"iter"

	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
	"github.com/authzed/spicedb-clients/spicedb-go/consistency"
	"github.com/authzed/spicedb-clients/spicedb-go/rel"
)

const (
	defaultImportBatchSize = 1000
	defaultExportPageSize  = 512
)

type ImportRelationshipsRequest struct{ Relationships iter.Seq[rel.Relationship] }
type ImportRelationshipsResult struct{ ImportedCount uint64 }

// ImportRelationships uses bounded batches with backpressure, and never retries uploads.
// If the producer panics, deferred cancellation releases the started upload.
//
// One call is one stream and one server transaction: either every relationship
// is imported or none is. Callers wanting several transactions make several
// calls. WithImportRelationshipsBatchSize sets relationships per message.
//
// When the server aborts the stream (for example ALREADY_EXISTS for a
// duplicate relationship), the error is the server's status, not the io.EOF
// the failed send reports.
func (c *Client) ImportRelationships(ctx context.Context, request ImportRelationshipsRequest, opts ...ImportRelationshipsOption) (ImportRelationshipsResult, error) {
	o := operationOptions{pageSize: defaultImportBatchSize}
	for _, opt := range opts {
		opt(&o)
	}
	if _, e := mutationRetry(o, false, "import relationships"); e != nil {
		return ImportRelationshipsResult{}, e
	}
	ctx, cancel, e := c.operation(ctx, o, true)
	if e != nil {
		return ImportRelationshipsResult{}, e
	}
	defer cancel()
	if request.Relationships == nil {
		return ImportRelationshipsResult{}, invalid("relationships iterator required")
	}
	stream, e := c.psc.ImportBulkRelationships(ctx, o.callOptions...)
	if e != nil {
		return ImportRelationshipsResult{}, mapGRPCError("import relationships", e)
	}
	batchSize := int(o.pageSize)
	batch := make([]*v1.Relationship, 0, batchSize)
	send := func() error {
		if e := ctx.Err(); e != nil {
			return mapGRPCError("import relationships", e)
		}
		if e := stream.Send(&v1.ImportBulkRelationshipsRequest{Relationships: batch}); e != nil {
			// A client-streaming Send that fails because the server ended
			// the stream reports io.EOF; the real status is only available
			// from CloseAndRecv.
			if _, closeErr := stream.CloseAndRecv(); closeErr != nil {
				e = closeErr
			}
			return mapGRPCError("import relationships", e)
		}
		batch = make([]*v1.Relationship, 0, batchSize)
		return nil
	}
	for r := range request.Relationships {
		if e := ctx.Err(); e != nil {
			return ImportRelationshipsResult{}, mapGRPCError("import relationships", e)
		}
		p, e := relationship(r)
		if e != nil {
			return ImportRelationshipsResult{}, e
		}
		batch = append(batch, p)
		if len(batch) == batchSize {
			if e := send(); e != nil {
				return ImportRelationshipsResult{}, e
			}
		}
	}
	if len(batch) > 0 {
		if e := send(); e != nil {
			return ImportRelationshipsResult{}, e
		}
	}
	resp, e := stream.CloseAndRecv()
	if e != nil {
		return ImportRelationshipsResult{}, mapGRPCError("import relationships", e)
	}
	return ImportRelationshipsResult{resp.GetNumLoaded()}, nil
}

type ExportRelationshipsRequest struct{ Consistency consistency.Strategy }

// ExportedRelationship is an exported relationship with its paging state. It
// embeds rel.Relationship; pass r.Relationship where one is required.
type ExportedRelationship struct {
	rel.Relationship
	// AfterResultCursor resumes an export after this item's response, via
	// WithExportRelationshipsCursor. Every item in one response shares it.
	AfterResultCursor string
}

func (c *Client) ExportRelationships(ctx context.Context, request ExportRelationshipsRequest, opts ...ExportRelationshipsOption) iter.Seq2[ExportedRelationship, error] {
	o := operationOptions{pageSize: defaultExportPageSize}
	for _, opt := range opts {
		opt(&o)
	}
	cs, ce := strategy(request.Consistency)
	var f *v1.RelationshipFilter
	var fe error
	if o.filter != nil {
		f, fe = filter(*o.filter)
	}
	return func(yield func(ExportedRelationship, error) bool) {
		ctx, cancel, e := c.operation(ctx, o, true)
		if e != nil {
			yield(ExportedRelationship{}, e)
			return
		}
		defer cancel()
		if ce != nil {
			yield(ExportedRelationship{}, ce)
			return
		}
		if fe != nil {
			yield(ExportedRelationship{}, fe)
			return
		}
		cur := cursor(o.cursor)
		var total uint64
		exposed := false
		for {
			size := o.pageSize
			if o.limit > 0 {
				size = uint32(min(uint64(size), o.limit-total))
			}
			req := &v1.ExportBulkRelationshipsRequest{Consistency: cs, OptionalLimit: size, OptionalCursor: cur, OptionalRelationshipFilter: f}
			pageCtx, pageCancel := context.WithCancel(ctx)
			count := uint32(0)
			prev := cur.GetToken()
			for resp, e := range receive(pageCtx, c.retrySettings(o), "export relationships", !exposed, func(ctx context.Context) (receiver[*v1.ExportBulkRelationshipsResponse], error) {
				return c.psc.ExportBulkRelationships(ctx, req, o.callOptions...)
			}) {
				if e != nil {
					pageCancel()
					yield(ExportedRelationship{}, e)
					return
				}
				cur = resp.GetAfterResultCursor()
				for _, p := range resp.GetRelationships() {
					r, e := nativeRelationship(p)
					if e != nil {
						pageCancel()
						yield(ExportedRelationship{}, e)
						return
					}
					count++
					total++
					exposed = true
					if !yield(ExportedRelationship{Relationship: r, AfterResultCursor: cur.GetToken()}, nil) {
						pageCancel()
						return
					}
					if o.limit > 0 && total >= o.limit {
						pageCancel()
						return
					}
				}
			}
			pageCancel()
			if count < size {
				return
			}
			if cur.GetToken() == "" || cur.GetToken() == prev {
				yield(ExportedRelationship{}, &Error{Code: CodeInternal, Message: "spicedb: export page did not advance cursor"})
				return
			}
		}
	}
}
