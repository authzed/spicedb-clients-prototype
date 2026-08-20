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
	defaultImportBatchSize = 1000
	defaultExportPageSize  = 512
)

// ImportRelationships streams relationships to SpiceDB for bulk import,
// returning the number of relationships loaded. Relationships are automatically
// batched into chunks of 1,000.
//
// If any relationship's caveat context cannot be converted to protobuf, the
// import stops immediately and returns a CodeInvalidArgument error naming
// the offending relationship — never a partial import written with a
// silently dropped context, which bulk import would otherwise persist at
// scale.
func (c *Client) ImportRelationships(ctx context.Context, rels iter.Seq[rel.Relationship]) (numLoaded uint64, err error) {
	// Returning early -- e.g. when a relationship's caveat context fails to
	// convert to protobuf, or when a Send fails mid-stream -- must still
	// release the client-streaming call opened below. grpc-go's
	// ClientConn.NewStream contract is explicit: unless the context is
	// cancelled, Close is called, or RecvMsg drains to a non-nil error, "a
	// goroutine and a context will be leaked", and SpiceDB keeps the
	// server-side dispatch open for as long as the connection lives.
	// Cancelling on the way out covers every exit path, including these
	// early returns, the same way ExportRelationships below covers its own.
	// See root DESIGN.md, "RULE: Abandoning a stream must release it".
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	stream, err := c.psc.ImportBulkRelationships(ctx)
	if err != nil {
		return 0, mapGRPCError("import relationships", err)
	}

	batch := make([]*v1.Relationship, 0, defaultImportBatchSize)

	for r := range rels {
		p, err := r.ToProto()
		if err != nil {
			return 0, &Error{
				Code:    CodeInvalidArgument,
				Message: fmt.Sprintf("spicedb: import relationships: %s", err),
				err:     err,
			}
		}
		batch = append(batch, p)
		if len(batch) >= defaultImportBatchSize {
			if err := stream.Send(&v1.ImportBulkRelationshipsRequest{
				Relationships: batch,
			}); err != nil {
				return 0, mapGRPCError("import relationships", err)
			}
			batch = batch[:0]
		}
	}

	// Send remaining batch
	if len(batch) > 0 {
		if err := stream.Send(&v1.ImportBulkRelationshipsRequest{
			Relationships: batch,
		}); err != nil {
			return 0, mapGRPCError("import relationships", err)
		}
	}

	resp, err := stream.CloseAndRecv()
	if err != nil {
		return 0, mapGRPCError("import relationships", err)
	}
	return resp.GetNumLoaded(), nil
}

// ExportRelationships returns an iterator over all relationships matching the
// optional filter, streamed from SpiceDB in bulk. Cursors are handled
// transparently — the client automatically re-fetches pages of 512
// relationships.
func (c *Client) ExportRelationships(ctx context.Context, cs consistency.Strategy, f *rel.Filter) iter.Seq2[rel.Relationship, error] {
	return func(yield func(rel.Relationship, error) bool) {
		// Abandoning this iterator -- a `break` in the consuming range
		// loop, or any early return -- must release the stream. grpc-go's
		// ClientConn.NewStream contract is explicit: unless the context is
		// cancelled, Close is called, or RecvMsg drains to a non-nil error,
		// "a goroutine and a context will be leaked", and SpiceDB keeps the
		// server-side dispatch open for as long as the connection lives.
		// Cancelling on the way out covers every exit path, including the
		// early return taken when the consumer breaks. See root DESIGN.md,
		// "RULE: Abandoning a stream must release it".
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()

		var filterProto *v1.RelationshipFilter
		if f != nil {
			p, err := f.ToProto()
			if err != nil {
				yield(rel.Relationship{}, &Error{
					Code:    CodeInvalidArgument,
					Message: fmt.Sprintf("spicedb: export relationships: %s", err),
					err:     err,
				})
				return
			}
			filterProto = p
		}

		var cursor *v1.Cursor
		for {
			req := &v1.ExportBulkRelationshipsRequest{
				Consistency:    cs.V1Consistency,
				OptionalLimit:  defaultExportPageSize,
				OptionalCursor: cursor,
			}
			if filterProto != nil {
				req.OptionalRelationshipFilter = filterProto
			}

			stream, err := c.psc.ExportBulkRelationships(ctx, req)
			if err != nil {
				yield(rel.Relationship{}, mapGRPCError("export relationships", err))
				return
			}

			var pageCount int
			for {
				resp, err := stream.Recv()
				if err == io.EOF {
					break
				}
				if err != nil {
					yield(rel.Relationship{}, mapGRPCError("export relationships", err))
					return
				}
				cursor = resp.GetAfterResultCursor()
				for _, r := range resp.GetRelationships() {
					pageCount++
					if !yield(rel.FromProto(r), nil) {
						return
					}
				}
			}

			if pageCount < defaultExportPageSize {
				return
			}
		}
	}
}
