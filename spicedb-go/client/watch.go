package client

import (
	"context"
	"io"
	"iter"

	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
	"github.com/authzed/spicedb-clients/spicedb-go/rel"
)

// WatchEvent is one event from Updates, corresponding to a single
// WatchResponse from the server.
//
// ChangesThrough is always populated -- proto: "the ZedToken that
// represents the point in time that the watch response is current
// through. This token can be used in a subsequent WatchRequest to resume
// watching from this point." Pass it as the startRevision argument to a
// later Updates call to resume after a dropped stream, instead of
// restarting from the original startRevision (reprocessing everything
// since, possibly past the GC window) or from head (silently losing every
// change in the gap).
//
// IsCheckpoint is true for a checkpoint event, which carries no Updates --
// it exists only to advertise a fresh ChangesThrough and, behind a proxy
// that aborts idle connections, to keep the stream alive. Checkpoints are
// only sent when WithIncludeCheckpoints is passed to Updates.
type WatchEvent struct {
	Updates        []rel.Update
	ChangesThrough string
	IsCheckpoint   bool
}

// WatchOption configures an optional aspect of an Updates call.
type WatchOption func(*watchOptions)

type watchOptions struct {
	includeCheckpoints bool
}

// WithIncludeCheckpoints requests periodic checkpoint events (WatchEvent
// with IsCheckpoint true and no Updates) in addition to relationship
// updates. Per the proto: recommended if this SpiceDB instance is running
// behind a proxy that aborts idle connections, since a checkpoint keeps the
// stream alive even when there are no changes.
func WithIncludeCheckpoints() WatchOption {
	return func(o *watchOptions) { o.includeCheckpoints = true }
}

// Updates returns an iterator over watch events from SpiceDB's watch API,
// starting from the given revision (or from head, if startRevision is
// empty). Each yielded WatchEvent corresponds to one server response: zero
// or more relationship updates, all current through ChangesThrough.
func (c *Client) Updates(ctx context.Context, objectTypes []string, startRevision string, opts ...WatchOption) iter.Seq2[WatchEvent, error] {
	var o watchOptions
	for _, opt := range opts {
		opt(&o)
	}

	return func(yield func(WatchEvent, error) bool) {
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

		req := &v1.WatchRequest{
			OptionalObjectTypes: objectTypes,
		}
		if startRevision != "" {
			req.OptionalStartCursor = &v1.ZedToken{Token: startRevision}
		}
		if o.includeCheckpoints {
			// OptionalUpdateKinds is empty-means-default (relationship
			// updates only, for backwards compatibility) -- a non-empty
			// list is the exact set requested, so asking for checkpoints
			// must also spell out relationship updates or the server would
			// stop sending them.
			req.OptionalUpdateKinds = []v1.WatchKind{
				v1.WatchKind_WATCH_KIND_INCLUDE_RELATIONSHIP_UPDATES,
				v1.WatchKind_WATCH_KIND_INCLUDE_CHECKPOINTS,
			}
		}

		stream, err := c.wsc.Watch(ctx, req)
		if err != nil {
			yield(WatchEvent{}, mapGRPCError("watch", err))
			return
		}

		for {
			resp, err := stream.Recv()
			if err == io.EOF {
				return
			}
			if err != nil {
				yield(WatchEvent{}, mapGRPCError("watch", err))
				return
			}

			protoUpdates := resp.GetUpdates()
			updates := make([]rel.Update, 0, len(protoUpdates))
			for _, update := range protoUpdates {
				updates = append(updates, rel.UpdateFromProto(update))
			}

			event := WatchEvent{
				Updates:        updates,
				ChangesThrough: resp.GetChangesThrough().GetToken(),
				IsCheckpoint:   resp.GetIsCheckpoint(),
			}
			if !yield(event, nil) {
				return
			}
		}
	}
}
