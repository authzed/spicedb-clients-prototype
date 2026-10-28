package client

import (
	"context"
	"iter"

	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
	"github.com/authzed/spicedb-clients/spicedb-go/consistency"
	"github.com/authzed/spicedb-clients/spicedb-go/rel"
)

type WatchEvent struct {
	Updates        []rel.Update
	ChangesThrough consistency.ZedToken
	IsCheckpoint   bool
	// SchemaUpdated reports a schema change at ChangesThrough. Sent only when
	// requested with WithUpdatesSchemaChanges(SchemaChangesIncluded).
	SchemaUpdated bool
}
type UpdatesRequest struct {
	// ObjectTypes cannot be combined with Filters.
	ObjectTypes []string
	// Filters are ORed by the server. Empty filters watch all relationships.
	Filters []rel.Filter
}

func (c *Client) Updates(ctx context.Context, request UpdatesRequest, opts ...UpdatesOption) iter.Seq2[WatchEvent, error] {
	o := operationOptions{}
	applyOptions(c, &o, opts)
	types := append([]string(nil), request.ObjectTypes...)
	var filters []*v1.RelationshipFilter
	var validationErr error
	if len(types) > 0 && len(request.Filters) > 0 {
		validationErr = invalid("object types and relationship filters cannot be combined")
	} else {
		// Convert now to snapshot the caller's filter slice before lazy iteration.
		for _, native := range request.Filters {
			converted, err := filter(native)
			if err != nil {
				validationErr = err
				break
			}
			filters = append(filters, converted)
		}
	}
	return func(yield func(WatchEvent, error) bool) {
		ctx, cancel, e := c.operation(ctx, o, true)
		if e != nil {
			yield(WatchEvent{}, e)
			return
		}
		defer cancel()
		if validationErr != nil {
			yield(WatchEvent{}, validationErr)
			return
		}
		req := &v1.WatchRequest{OptionalObjectTypes: types, OptionalRelationshipFilters: filters}
		if o.start.String() != "" {
			req.OptionalStartCursor = &v1.ZedToken{Token: o.start.String()}
		}
		if o.checkpoints || o.schemaChanges {
			req.OptionalUpdateKinds = []v1.WatchKind{v1.WatchKind_WATCH_KIND_INCLUDE_RELATIONSHIP_UPDATES}
			if o.checkpoints {
				req.OptionalUpdateKinds = append(req.OptionalUpdateKinds, v1.WatchKind_WATCH_KIND_INCLUDE_CHECKPOINTS)
			}
			if o.schemaChanges {
				req.OptionalUpdateKinds = append(req.OptionalUpdateKinds, v1.WatchKind_WATCH_KIND_INCLUDE_SCHEMA_UPDATES)
			}
		}
		for resp, e := range receive(ctx, c.retrySettings(o), "watch", true, func(ctx context.Context) (receiver[*v1.WatchResponse], error) {
			return c.wsc.Watch(ctx, req, o.callOptions...)
		}) {
			if e != nil {
				yield(WatchEvent{}, e)
				return
			}
			event := WatchEvent{ChangesThrough: consistency.NewZedToken(resp.GetChangesThrough().GetToken()), IsCheckpoint: resp.GetIsCheckpoint(), SchemaUpdated: resp.GetSchemaUpdated()}
			for _, p := range resp.GetUpdates() {
				u, e := nativeUpdate(p)
				if e != nil {
					yield(WatchEvent{}, e)
					return
				}
				event.Updates = append(event.Updates, u)
			}
			if !yield(event, nil) {
				return
			}
		}
	}
}
