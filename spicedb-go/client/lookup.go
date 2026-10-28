package client

import (
	"context"
	"iter"

	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
	"github.com/authzed/spicedb-clients/spicedb-go/consistency"
	"github.com/authzed/spicedb-clients/spicedb-go/rel"
)

const defaultLookupPageSize = 512

type LookupResourcesRequest struct {
	ResourceType, Permission string
	Subject                  rel.SubjectRef
	Consistency              consistency.Strategy
}

func (c *Client) LookupResources(ctx context.Context, request LookupResourcesRequest, opts ...LookupResourcesOption) iter.Seq2[LookupResource, error] {
	o := operationOptions{pageSize: defaultLookupPageSize}
	applyOptions(c, &o, opts)
	initialConsistency, ce := strategy(request.Consistency)
	ctxStruct, je := contextStruct(o.checkContext)
	if request.ResourceType == "" || request.Permission == "" || request.Subject.Validate() != nil {
		ce = invalid("resource type, permission and valid subject required")
	}
	return func(yield func(LookupResource, error) bool) {
		cs := initialConsistency
		ctx, cancel, e := c.operation(ctx, o, true)
		if e != nil {
			yield(LookupResource{}, e)
			return
		}
		defer cancel()
		if ce != nil {
			yield(LookupResource{}, ce)
			return
		}
		if je != nil {
			yield(LookupResource{}, je)
			return
		}
		cur := cursor(o.cursor)
		var total uint64
		exposed := false
		// Every page repeats the caller's consistency and one page size:
		// SpiceDB rejects a cursor sent with different arguments, and the
		// cursor pins later pages to the first page's revision. The total
		// limit is enforced here.
		size := o.pageRequestLimit()
		for {
			req := &v1.LookupResourcesRequest{Consistency: cs, ResourceObjectType: request.ResourceType, Permission: request.Permission, Subject: subject(request.Subject), Context: ctxStruct, OptionalLimit: size, OptionalCursor: cur, WithDebug: o.debug}
			pageCtx, pageCancel := context.WithCancel(ctx)
			count := uint32(0)
			prev := cur.GetToken()
			for resp, e := range receive(pageCtx, c.retrySettings(o), "lookup resources", !exposed, func(ctx context.Context) (receiver[*v1.LookupResourcesResponse], error) {
				return c.psc.LookupResources(ctx, req, o.callOptions...)
			}) {
				if e != nil {
					pageCancel()
					yield(LookupResource{}, e)
					return
				}
				count++
				cur = resp.GetAfterResultCursor()
				exposed = true
				if o.lookupPermissionshipFilter == LookupHasPermissionOnly && resp.GetPermissionship() != v1.LookupPermissionship_LOOKUP_PERMISSIONSHIP_HAS_PERMISSION {
					continue
				}
				total++
				r := LookupResource{ResourceID: resp.GetResourceObjectId(), Permissionship: permissionshipFromProto(resp.GetPermissionship()), PartialCaveat: partialCaveatFromProto(resp.GetPartialCaveatInfo()), LookedUpAt: consistency.NewZedToken(resp.GetLookedUpAt().GetToken()), AfterResultCursor: cur.GetToken()}
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
			if o.singleRequest || count < size {
				return
			}
			if cur.GetToken() == "" || cur.GetToken() == prev {
				yield(LookupResource{}, &Error{Code: CodeInternal, Message: "spicedb: lookup page did not advance cursor"})
				return
			}
		}
	}
}

type LookupSubjectsRequest struct {
	Resource                rel.ObjectRef
	Permission, SubjectType string
	Consistency             consistency.Strategy
}

// LookupSubjects returns wildcard exclusions verbatim. Callers must evaluate exclusions before granting a wildcard.
// This wrapper consumes a single stream and does not invent subject cursors.
func (c *Client) LookupSubjects(ctx context.Context, request LookupSubjectsRequest, opts ...LookupSubjectsOption) iter.Seq2[LookupSubject, error] {
	o := operationOptions{}
	applyOptions(c, &o, opts)
	initialConsistency, ce := strategy(request.Consistency)
	ctxStruct, je := contextStruct(o.checkContext)
	if request.Resource.Validate() != nil || request.Permission == "" || request.SubjectType == "" {
		ce = invalid("valid resource, permission and subject type required")
	}
	return func(yield func(LookupSubject, error) bool) {
		cs := initialConsistency
		ctx, cancel, e := c.operation(ctx, o, true)
		if e != nil {
			yield(LookupSubject{}, e)
			return
		}
		defer cancel()
		if ce != nil {
			yield(LookupSubject{}, ce)
			return
		}
		if je != nil {
			yield(LookupSubject{}, je)
			return
		}
		req := &v1.LookupSubjectsRequest{Consistency: cs, Resource: object(request.Resource), Permission: request.Permission, SubjectObjectType: request.SubjectType, OptionalSubjectRelation: o.subjectRelation, Context: ctxStruct}
		if o.limit > 0 && o.limit <= uint64(^uint32(0)) {
			req.OptionalConcreteLimit = uint32(o.limit)
		}
		var count uint64
		for resp, e := range receive(ctx, c.retrySettings(o), "lookup subjects", true, func(ctx context.Context) (receiver[*v1.LookupSubjectsResponse], error) {
			return c.psc.LookupSubjects(ctx, req, o.callOptions...)
		}) {
			if e != nil {
				yield(LookupSubject{}, e)
				return
			}
			s := resolvedSubjectFromProto(resp.GetSubject())
			if s.SubjectID == "" {
				s = ResolvedSubject{SubjectID: resp.GetSubjectObjectId(), Permissionship: permissionshipFromProto(resp.GetPermissionship()), PartialCaveat: partialCaveatFromProto(resp.GetPartialCaveatInfo())} //nolint:staticcheck // Support older server response fields.
			}
			var excluded []ResolvedSubject
			for _, p := range resp.GetExcludedSubjects() {
				excluded = append(excluded, resolvedSubjectFromProto(p))
			}
			if len(excluded) == 0 {
				for _, id := range resp.GetExcludedSubjectIds() { //nolint:staticcheck // Support older server response fields.
					excluded = append(excluded, ResolvedSubject{SubjectID: id})
				}
			}
			count++
			if !yield(LookupSubject{Subject: s, ExcludedSubjects: excluded, LookedUpAt: consistency.NewZedToken(resp.GetLookedUpAt().GetToken())}, nil) {
				return
			}
			if o.limit > 0 && count >= o.limit {
				return
			}
		}
	}
}
