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
		// limit is enforced here. The response batch size is not part of the
		// cursor, so it may change from page to page.
		size := o.pageRequestLimit()
		for {
			req := &v1.LookupResourcesRequest{Consistency: cs, ResourceObjectType: request.ResourceType, Permission: request.Permission, Subject: subject(request.Subject), Context: ctxStruct, OptionalLimit: size, OptionalCursor: cur, WithDebug: o.debug, OptionalResponseBatchSize: o.pageBatchSize(total)}
			// The page's clock pauses while the caller holds an item.
			pageCtx, clock, pageCancel := c.serverClock(ctx, true)
			count := uint32(0)
			prev := cur.GetToken()
			for resp, e := range receive(pageCtx, c.retrySettings(o), "lookup resources", !exposed, func(ctx context.Context) (receiver[*v1.LookupResourcesResponse], error) {
				return c.psc.LookupResources(ctx, req, o.callOptions...)
			}) {
				clock.hold()
				if e != nil {
					e = clock.err(pageCtx, "lookup resources", e)
					pageCancel()
					yield(LookupResource{}, e)
					return
				}
				exposed = true
				// A response is either one resource with its own cursor or a
				// batch with a single cursor after its last result. Pages
				// count results, not messages.
				results, last := lookupResults(resp, o.lookupPermissionshipFilter)
				if resp.GetBatched() == nil || len(results) > 0 || resp.GetBatched().GetAfterCursor() != nil {
					cur = lookupCursor(resp)
				}
				lookedUpAt := consistency.NewZedToken(resp.GetLookedUpAt().GetToken())
				for i, result := range results {
					count++
					if result.GetResourceObjectId() == "" {
						pageCancel()
						yield(LookupResource{}, malformed("lookup resources", "resource object id is missing"))
						return
					}
					if result.GetPermissionship() == v1.LookupPermissionship_LOOKUP_PERMISSIONSHIP_UNSPECIFIED {
						pageCancel()
						yield(LookupResource{}, malformed("lookup resources", "permissionship is unset"))
						return
					}
					if !lookupKept(result, o.lookupPermissionshipFilter) {
						continue
					}
					total++
					r := LookupResource{ResourceID: result.GetResourceObjectId(), Permissionship: permissionshipFromProto(result.GetPermissionship()), PartialCaveat: partialCaveatFromProto(result.GetPartialCaveatInfo()), LookedUpAt: lookedUpAt}
					if i == last {
						r.AfterResultCursor = cur.GetToken()
					}
					clock.hold()
					if !yield(r, nil) {
						pageCancel()
						return
					}
					clock.wait()
					if o.limit > 0 && total >= o.limit {
						pageCancel()
						return
					}
				}
				// A response that yielded nothing still waits on the server next.
				clock.wait()
			}
			pageCancel()
			if o.singleRequest || count < size {
				return
			}
			if cur.GetToken() == "" || cur.GetToken() == prev {
				yield(LookupResource{}, malformed("lookup resources", "page did not advance cursor"))
				return
			}
		}
	}
}

// lookupResult is the part of a looked-up resource that both wire forms
// carry: a one-resource response and a result inside a batch.
type lookupResult interface {
	GetResourceObjectId() string
	GetPermissionship() v1.LookupPermissionship
	GetPartialCaveatInfo() *v1.PartialCaveatInfo
}

// lookupResults returns a response's results in order, whichever form it
// arrived in, and the index of the one that carries the response's cursor:
// the last result the filter keeps, or -1 when it keeps none. An empty batch
// has no results.
func lookupResults(resp *v1.LookupResourcesResponse, filter LookupPermissionshipFilter) ([]lookupResult, int) {
	results := []lookupResult{resp}
	if batch := resp.GetBatched(); batch != nil {
		results = make([]lookupResult, len(batch.GetResults()))
		for i, r := range batch.GetResults() {
			results[i] = r
		}
	}
	for i := len(results) - 1; i >= 0; i-- {
		if lookupKept(results[i], filter) {
			return results, i
		}
	}
	return results, -1
}

// lookupCursor is the cursor after a response's last result.
func lookupCursor(resp *v1.LookupResourcesResponse) *v1.Cursor {
	if batch := resp.GetBatched(); batch != nil {
		return batch.GetAfterCursor()
	}
	return resp.GetAfterResultCursor()
}

// lookupKept reports whether the permissionship filter yields a result.
func lookupKept(r lookupResult, filter LookupPermissionshipFilter) bool {
	return filter != LookupHasPermissionOnly || r.GetPermissionship() == v1.LookupPermissionship_LOOKUP_PERMISSIONSHIP_HAS_PERMISSION
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
		// LookupSubjects is one stream with no pages: the client default
		// deadline bounds each wait for the next message, never the time the
		// caller holds an item.
		ctx, clock, release := c.serverClock(ctx, false)
		defer release()
		var count uint64
		for resp, e := range receive(ctx, c.retrySettings(o), "lookup subjects", true, func(ctx context.Context) (receiver[*v1.LookupSubjectsResponse], error) {
			return c.psc.LookupSubjects(ctx, req, o.callOptions...)
		}) {
			clock.hold()
			if e != nil {
				yield(LookupSubject{}, clock.err(ctx, "lookup subjects", e))
				return
			}
			s := resolvedSubjectFromProto(resp.GetSubject())
			if s.SubjectID == "" {
				s = ResolvedSubject{SubjectID: resp.GetSubjectObjectId(), Permissionship: permissionshipFromProto(resp.GetPermissionship()), PartialCaveat: partialCaveatFromProto(resp.GetPartialCaveatInfo())} //nolint:staticcheck // Support older server response fields.
			}
			if s.SubjectID == "" {
				yield(LookupSubject{}, malformed("lookup subjects", "subject is missing"))
				return
			}
			var excluded []ResolvedSubject
			for _, p := range resp.GetExcludedSubjects() {
				x := resolvedSubjectFromProto(p)
				if x.SubjectID == "" {
					yield(LookupSubject{}, malformed("lookup subjects", "excluded subject id is missing"))
					return
				}
				excluded = append(excluded, x)
			}
			if len(excluded) == 0 {
				for _, id := range resp.GetExcludedSubjectIds() { //nolint:staticcheck // Support older server response fields.
					if id == "" {
						yield(LookupSubject{}, malformed("lookup subjects", "excluded subject id is missing"))
						return
					}
					excluded = append(excluded, ResolvedSubject{SubjectID: id})
				}
			}
			if rawSubjectPermissionship(resp) == v1.LookupPermissionship_LOOKUP_PERMISSIONSHIP_UNSPECIFIED {
				yield(LookupSubject{}, malformed("lookup subjects", "permissionship is unset"))
				return
			}
			count++
			if !yield(LookupSubject{Subject: s, ExcludedSubjects: excluded, LookedUpAt: consistency.NewZedToken(resp.GetLookedUpAt().GetToken())}, nil) {
				return
			}
			clock.wait()
			if o.limit > 0 && count >= o.limit {
				return
			}
		}
	}
}

// rawSubjectPermissionship is the wire permissionship of a lookup-subjects
// response's main subject, from the current field or the older one.
func rawSubjectPermissionship(resp *v1.LookupSubjectsResponse) v1.LookupPermissionship {
	if resp.GetSubject() != nil {
		return resp.GetSubject().GetPermissionship()
	}
	return resp.GetPermissionship() //nolint:staticcheck // Support older server response fields.
}
