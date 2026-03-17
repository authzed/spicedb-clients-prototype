package client

import (
	"context"
	"fmt"
	"io"
	"iter"

	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
	"github.com/authzed/spicedb-clients/spicedb-go/rel"
)

// Updates returns an iterator over relationship changes from SpiceDB's watch
// API, starting from the given revision. Each yielded Update contains the
// operation (create/touch/delete) and the affected relationship.
func (c *Client) Updates(ctx context.Context, objectTypes []string, startRevision string) iter.Seq2[rel.Update, error] {
	return func(yield func(rel.Update, error) bool) {
		req := &v1.WatchRequest{
			OptionalObjectTypes: objectTypes,
		}
		if startRevision != "" {
			req.OptionalStartCursor = &v1.ZedToken{Token: startRevision}
		}

		stream, err := c.wsc.Watch(ctx, req)
		if err != nil {
			yield(rel.Update{}, fmt.Errorf("spicedb: watch: %w", err))
			return
		}

		for {
			resp, err := stream.Recv()
			if err == io.EOF {
				return
			}
			if err != nil {
				yield(rel.Update{}, fmt.Errorf("spicedb: watch: %w", err))
				return
			}

			for _, update := range resp.GetUpdates() {
				if !yield(rel.UpdateFromProto(update), nil) {
					return
				}
			}
		}
	}
}
