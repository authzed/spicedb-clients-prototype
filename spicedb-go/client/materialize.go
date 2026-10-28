package client

import (
	"context"
	materialize "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/materialize/v0"
	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
	"github.com/authzed/spicedb-clients/spicedb-go/consistency"
	"github.com/authzed/spicedb-clients/spicedb-go/rel"
	"iter"
	"time"
)

type CountRelationshipsByFilterRequest struct{ Filter rel.Filter }
type CountRelationshipsByFilterResult struct {
	RelationshipCount uint64
	Revision          consistency.ZedToken
}
type CountRelationshipsByFilterOption func(*operationOptions)

func WithCountRelationshipsByFilterTimeout(d time.Duration) CountRelationshipsByFilterOption {
	return func(o *operationOptions) { timeoutOption(o, d) }
}

// Experimental: CountRelationshipsByFilter reads the materialize relationship count on the authenticated connection.
func (c *Client) CountRelationshipsByFilter(ctx context.Context, request CountRelationshipsByFilterRequest, opts ...CountRelationshipsByFilterOption) (CountRelationshipsByFilterResult, error) {
	o := operationOptions{}
	for _, opt := range opts {
		opt(&o)
	}
	ctx, cancel, e := c.operation(ctx, o, false)
	if e != nil {
		return CountRelationshipsByFilterResult{}, e
	}
	defer cancel()
	f, e := filter(request.Filter)
	if e != nil {
		return CountRelationshipsByFilterResult{}, e
	}
	stub := materialize.NewRelationshipsServiceClient(c.proto.Connection())
	response, e := retry(ctx, c.retrySettings(o), "count relationships by filter", func() (*materialize.ExperimentalCountRelationshipsByFilterResponse, error) {
		return stub.ExperimentalCountRelationshipsByFilter(ctx, &materialize.ExperimentalCountRelationshipsByFilterRequest{RelationshipFilter: f}, o.callOptions...)
	})
	if e != nil {
		return CountRelationshipsByFilterResult{}, e
	}
	return CountRelationshipsByFilterResult{response.GetRelationshipCount(), consistency.NewZedToken(response.GetReadAt().GetToken())}, nil
}

type PermissionSetOperation uint8

const (
	PermissionSetOperationUnspecified PermissionSetOperation = iota
	PermissionSetAdded
	PermissionSetRemoved
)

type PermissionSetReference struct {
	Object               rel.ObjectRef
	PermissionOrRelation string
}
type PermissionSetMember struct {
	Object               rel.ObjectRef
	PermissionOrRelation string
}
type PermissionSetChildKind uint8

const (
	PermissionSetChildUnknown PermissionSetChildKind = iota
	PermissionSetChildSet
	PermissionSetChildMember
)

type PermissionSetChange struct {
	Revision    consistency.ZedToken
	Operation   PermissionSetOperation
	Parent      PermissionSetReference
	ChildKind   PermissionSetChildKind
	ChildSet    *PermissionSetReference
	ChildMember *PermissionSetMember
}
type PermissionSetEventKind uint8

const (
	PermissionSetEventUnknown PermissionSetEventKind = iota
	PermissionSetEventChange
	PermissionSetEventCompletedRevision
	PermissionSetEventLookupRequired
	PermissionSetEventBreakingSchemaChange
)

type WatchedPermission struct{ ResourceType, Permission, SubjectType, SubjectRelation string }
type PermissionSetEvent struct {
	Kind                PermissionSetEventKind
	Change              *PermissionSetChange
	Revision            consistency.ZedToken
	AffectedPermissions []WatchedPermission
}

// PermissionSetCursor is an opaque immutable snapshot of every materialize cursor field.
type PermissionSetCursor struct {
	limit, startingIndex uint32
	token                consistency.ZedToken
	completedMembers     bool
	startingKey, cursor  string
}

func (c PermissionSetCursor) Revision() consistency.ZedToken { return c.token }
func (c PermissionSetCursor) Complete() bool                 { return c.completedMembers }
func (c PermissionSetCursor) Token() string                  { return c.cursor }
func nativePermissionSetCursor(p *materialize.Cursor) PermissionSetCursor {
	return PermissionSetCursor{p.GetLimit(), p.GetStartingIndex(), consistency.NewZedToken(p.GetToken().GetToken()), p.GetCompletedMembers(), p.GetStartingKey(), p.GetCursor()}
}
func permissionSetCursor(c PermissionSetCursor) *materialize.Cursor {
	return &materialize.Cursor{Limit: c.limit, StartingIndex: c.startingIndex, Token: optionalToken(c.token), CompletedMembers: c.completedMembers, StartingKey: c.startingKey, Cursor: c.cursor}
}
func optionalToken(token consistency.ZedToken) *v1.ZedToken {
	if token.String() == "" {
		return nil
	}
	return &v1.ZedToken{Token: token.String()}
}
func nativeSet(p *materialize.SetReference) PermissionSetReference {
	return PermissionSetReference{Object: nativeObject(&v1.ObjectReference{ObjectType: p.GetObjectType(), ObjectId: p.GetObjectId()}), PermissionOrRelation: p.GetPermissionOrRelation()}
}
func nativePermissionSetChange(p *materialize.PermissionSetChange) *PermissionSetChange {
	if p == nil {
		return nil
	}
	r := &PermissionSetChange{Revision: consistency.NewZedToken(p.GetAtRevision().GetToken()), Parent: nativeSet(p.GetParentSet())}
	switch p.GetOperation() {
	case materialize.PermissionSetChange_SET_OPERATION_ADDED:
		r.Operation = PermissionSetAdded
	case materialize.PermissionSetChange_SET_OPERATION_REMOVED:
		r.Operation = PermissionSetRemoved
	}
	switch child := p.GetChild().(type) {
	case *materialize.PermissionSetChange_ChildSet:
		s := nativeSet(child.ChildSet)
		r.ChildKind = PermissionSetChildSet
		r.ChildSet = &s
	case *materialize.PermissionSetChange_ChildMember:
		r.ChildKind = PermissionSetChildMember
		r.ChildMember = &PermissionSetMember{Object: nativeObject(&v1.ObjectReference{ObjectType: child.ChildMember.GetObjectType(), ObjectId: child.ChildMember.GetObjectId()}), PermissionOrRelation: child.ChildMember.GetOptionalPermissionOrRelation()}
	}
	return r
}
func nativePermissionSetEvent(p *materialize.WatchPermissionSetsResponse) PermissionSetEvent {
	event := PermissionSetEvent{}
	switch value := p.GetResponse().(type) {
	case *materialize.WatchPermissionSetsResponse_Change:
		event.Kind = PermissionSetEventChange
		event.Change = nativePermissionSetChange(value.Change)
		if event.Change != nil {
			event.Revision = event.Change.Revision
		} else {
			event.Kind = PermissionSetEventUnknown
		}
	case *materialize.WatchPermissionSetsResponse_CompletedRevision:
		event.Kind = PermissionSetEventCompletedRevision
		event.Revision = consistency.NewZedToken(value.CompletedRevision.GetToken())
	case *materialize.WatchPermissionSetsResponse_LookupPermissionSetsRequired:
		event.Kind = PermissionSetEventLookupRequired
		event.Revision = consistency.NewZedToken(value.LookupPermissionSetsRequired.GetRequiredLookupAt().GetToken())
	case *materialize.WatchPermissionSetsResponse_BreakingSchemaChange:
		event.Kind = PermissionSetEventBreakingSchemaChange
		event.Revision = consistency.NewZedToken(value.BreakingSchemaChange.GetChangeAt().GetToken())
		for _, p := range value.BreakingSchemaChange.GetAffectedPermissions() {
			event.AffectedPermissions = append(event.AffectedPermissions, WatchedPermission{p.GetResourceType(), p.GetPermission(), p.GetSubjectType(), p.GetOptionalSubjectRelation()})
		}
	}
	return event
}

type WatchPermissionSetsRequest struct{}
type WatchPermissionSetsOption func(*operationOptions)

func WithWatchPermissionSetsTimeout(d time.Duration) WatchPermissionSetsOption {
	return func(o *operationOptions) { timeoutOption(o, d) }
}
func WithWatchPermissionSetsStart(token consistency.ZedToken) WatchPermissionSetsOption {
	return func(o *operationOptions) {
		if token.String() == "" {
			o.err = invalid("watch starting token must be nonempty")
			return
		}
		o.start = token
	}
}

// Experimental: WatchPermissionSets streams native normalized permission-set changes. Cancel ctx to interrupt a pending receive.
func (c *Client) WatchPermissionSets(ctx context.Context, request WatchPermissionSetsRequest, opts ...WatchPermissionSetsOption) iter.Seq2[PermissionSetEvent, error] {
	o := operationOptions{}
	for _, opt := range opts {
		opt(&o)
	}
	return func(yield func(PermissionSetEvent, error) bool) {
		ctx, cancel, e := c.operation(ctx, o, true)
		if e != nil {
			yield(PermissionSetEvent{}, e)
			return
		}
		defer cancel()
		stub := materialize.NewWatchPermissionSetsServiceClient(c.proto.Connection())
		for p, e := range receive(ctx, c.retrySettings(o), "watch permission sets", true, func(ctx context.Context) (receiver[*materialize.WatchPermissionSetsResponse], error) {
			return stub.WatchPermissionSets(ctx, &materialize.WatchPermissionSetsRequest{OptionalStartingAfter: optionalToken(o.start)}, o.callOptions...)
		}) {
			if e != nil {
				yield(PermissionSetEvent{}, e)
				return
			}
			if !yield(nativePermissionSetEvent(p), nil) {
				return
			}
		}
	}
}

type LookupPermissionSetsRequest struct{ Limit uint32 }
type LookupPermissionSet struct {
	Change *PermissionSetChange
	Cursor PermissionSetCursor
}
type LookupPermissionSetsOption func(*operationOptions)

func WithLookupPermissionSetsTimeout(d time.Duration) LookupPermissionSetsOption {
	return func(o *operationOptions) { timeoutOption(o, d) }
}
func WithLookupPermissionSetsRevision(token consistency.ZedToken) LookupPermissionSetsOption {
	return func(o *operationOptions) {
		if token.String() == "" {
			o.err = invalid("lookup revision must be nonempty")
			return
		}
		o.start = token
	}
}
func WithLookupPermissionSetsCursor(cursor PermissionSetCursor) LookupPermissionSetsOption {
	return func(o *operationOptions) { o.permissionSetCursor = &cursor }
}

// Experimental: LookupPermissionSets streams one bounded materialize page. Resume with the returned native Cursor.
func (c *Client) LookupPermissionSets(ctx context.Context, request LookupPermissionSetsRequest, opts ...LookupPermissionSetsOption) iter.Seq2[LookupPermissionSet, error] {
	o := operationOptions{}
	for _, opt := range opts {
		opt(&o)
	}
	if request.Limit == 0 {
		o.err = invalid("permission set lookup limit must be positive")
	}
	return func(yield func(LookupPermissionSet, error) bool) {
		ctx, cancel, e := c.operation(ctx, o, true)
		if e != nil {
			yield(LookupPermissionSet{}, e)
			return
		}
		defer cancel()
		req := &materialize.LookupPermissionSetsRequest{Limit: request.Limit, OptionalAtRevision: optionalToken(o.start)}
		if o.permissionSetCursor != nil {
			req.OptionalStartingAfterCursor = permissionSetCursor(*o.permissionSetCursor)
		}
		stub := materialize.NewWatchPermissionSetsServiceClient(c.proto.Connection())
		for p, e := range receive(ctx, c.retrySettings(o), "lookup permission sets", true, func(ctx context.Context) (receiver[*materialize.LookupPermissionSetsResponse], error) {
			return stub.LookupPermissionSets(ctx, req, o.callOptions...)
		}) {
			if e != nil {
				yield(LookupPermissionSet{}, e)
				return
			}
			if !yield(LookupPermissionSet{nativePermissionSetChange(p.GetChange()), nativePermissionSetCursor(p.GetCursor())}, nil) {
				return
			}
		}
	}
}

type DownloadPermissionSetsRequest struct{}
type PermissionSetFile struct{ Name, URL string }
type DownloadPermissionSetsResult struct {
	Files     []PermissionSetFile
	Timestamp *time.Time
	Revision  consistency.ZedToken
}
type DownloadPermissionSetsOption func(*operationOptions)

func WithDownloadPermissionSetsTimeout(d time.Duration) DownloadPermissionSetsOption {
	return func(o *operationOptions) { timeoutOption(o, d) }
}
func WithDownloadPermissionSetsRevision(token consistency.ZedToken) DownloadPermissionSetsOption {
	return func(o *operationOptions) {
		if token.String() == "" {
			o.err = invalid("download revision must be nonempty")
			return
		}
		o.start = token
	}
}

// Experimental: DownloadPermissionSets returns native file metadata. It does not download URLs or send credentials to them.
func (c *Client) DownloadPermissionSets(ctx context.Context, request DownloadPermissionSetsRequest, opts ...DownloadPermissionSetsOption) (DownloadPermissionSetsResult, error) {
	o := operationOptions{}
	for _, opt := range opts {
		opt(&o)
	}
	ctx, cancel, e := c.operation(ctx, o, false)
	if e != nil {
		return DownloadPermissionSetsResult{}, e
	}
	defer cancel()
	stub := materialize.NewWatchPermissionSetsServiceClient(c.proto.Connection())
	p, e := retry(ctx, c.retrySettings(o), "download permission sets", func() (*materialize.DownloadPermissionSetsResponse, error) {
		return stub.DownloadPermissionSets(ctx, &materialize.DownloadPermissionSetsRequest{OptionalAtRevision: optionalToken(o.start)}, o.callOptions...)
	})
	if e != nil {
		return DownloadPermissionSetsResult{}, e
	}
	result := DownloadPermissionSetsResult{Revision: consistency.NewZedToken(p.GetAtRevision().GetToken())}
	if ts := p.GetTimestamp(); ts != nil {
		t := ts.AsTime()
		result.Timestamp = &t
	}
	for _, file := range p.GetFiles() {
		result.Files = append(result.Files, PermissionSetFile{file.GetName(), file.GetUrl()})
	}
	return result, nil
}
