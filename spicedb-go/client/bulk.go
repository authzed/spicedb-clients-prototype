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
	defaultImportBatchSize = 1000
	defaultExportPageSize  = 512
)

// ImportRelationships streams relationships to SpiceDB for bulk import,
// returning the number of relationships loaded. Relationships are automatically
// batched into chunks of 1,000.
func (c *Client) ImportRelationships(ctx context.Context, rels iter.Seq[rel.Relationship]) (numLoaded uint64, err error) {
	stream, err := c.psc.ImportBulkRelationships(ctx)
	if err != nil {
		return 0, mapGRPCError("import relationships", err)
	}

	batch := make([]*v1.Relationship, 0, defaultImportBatchSize)

	for r := range rels {
		batch = append(batch, r.ToProto())
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
		var cursor *v1.Cursor
		for {
			req := &v1.ExportBulkRelationshipsRequest{
				Consistency:    cs.V1Consistency,
				OptionalLimit:  defaultExportPageSize,
				OptionalCursor: cursor,
			}
			if f != nil {
				req.OptionalRelationshipFilter = f.ToProto()
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
