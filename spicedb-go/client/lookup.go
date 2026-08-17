package client

import (
	"context"
	"io"
	"iter"

	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
	"github.com/authzed/spicedb-clients/spicedb-go/consistency"
)

const defaultLookupPageSize = 512

// LookupResources returns an iterator over resources of the given type that
// the subject has the specified permission on. Each result carries the
// permissionship (full grant vs conditional on caveat context) and, for
// conditional results, which caveat context was missing. Cursors are
// handled transparently — the client automatically re-fetches pages of 512
// results.
func (c *Client) LookupResources(ctx context.Context, cs consistency.Strategy, resourceType, permission, subjectType, subjectID string) iter.Seq2[LookupResource, error] {
	return func(yield func(LookupResource, error) bool) {
		var cursor *v1.Cursor
		for {
			stream, err := c.psc.LookupResources(ctx, &v1.LookupResourcesRequest{
				Consistency:        cs.V1Consistency,
				ResourceObjectType: resourceType,
				Permission:         permission,
				Subject: &v1.SubjectReference{
					Object: &v1.ObjectReference{
						ObjectType: subjectType,
						ObjectId:   subjectID,
					},
				},
				OptionalLimit:  uint32(defaultLookupPageSize),
				OptionalCursor: cursor,
			})
			if err != nil {
				yield(LookupResource{}, mapGRPCError("lookup resources", err))
				return
			}

			var count int
			for {
				resp, err := stream.Recv()
				if err == io.EOF {
					break
				}
				if err != nil {
					yield(LookupResource{}, mapGRPCError("lookup resources", err))
					return
				}
				count++
				cursor = resp.GetAfterResultCursor()
				result := LookupResource{
					ResourceID:     resp.GetResourceObjectId(),
					Permissionship: permissionshipFromProto(resp.GetPermissionship()),
					PartialCaveat:  partialCaveatFromProto(resp.GetPartialCaveatInfo()),
					LookedUpAt:     resp.GetLookedUpAt().GetToken(),
				}
				if !yield(result, nil) {
					return
				}
			}

			if count < defaultLookupPageSize {
				return
			}
		}
	}
}

// LookupSubjects returns an iterator over subjects of the given type that
// have the specified permission on the resource. Unlike LookupResources,
// LookupSubjects does not currently support cursor-based pagination in
// SpiceDB and streams all results in a single server-streaming call.
//
// When a yielded LookupSubject.Subject is the wildcard "*", the server has
// granted the permission to every subject of subjectType EXCEPT those
// listed in LookupSubject.ExcludedSubjects. Callers MUST check
// ExcludedSubjects before treating a wildcard match as a blanket grant, or
// they risk granting access to subjects the server explicitly excluded.
func (c *Client) LookupSubjects(ctx context.Context, cs consistency.Strategy, resourceType, resourceID, permission, subjectType string) iter.Seq2[LookupSubject, error] {
	return func(yield func(LookupSubject, error) bool) {
		stream, err := c.psc.LookupSubjects(ctx, &v1.LookupSubjectsRequest{
			Consistency: cs.V1Consistency,
			Resource: &v1.ObjectReference{
				ObjectType: resourceType,
				ObjectId:   resourceID,
			},
			Permission:        permission,
			SubjectObjectType: subjectType,
		})
		if err != nil {
			yield(LookupSubject{}, mapGRPCError("lookup subjects", err))
			return
		}

		for {
			resp, err := stream.Recv()
			if err == io.EOF {
				return
			}
			if err != nil {
				yield(LookupSubject{}, mapGRPCError("lookup subjects", err))
				return
			}

			subject := resolvedSubjectFromProto(resp.GetSubject())
			if subject.SubjectID == "" {
				// Fall back to the deprecated top-level fields for servers that
				// don't yet populate the non-deprecated `subject` field.
				subject = ResolvedSubject{
					SubjectID:      resp.GetSubjectObjectId(),                           //nolint:staticcheck
					Permissionship: permissionshipFromProto(resp.GetPermissionship()),   //nolint:staticcheck
					PartialCaveat:  partialCaveatFromProto(resp.GetPartialCaveatInfo()), //nolint:staticcheck
				}
			}

			var excluded []ResolvedSubject
			if protoExcluded := resp.GetExcludedSubjects(); len(protoExcluded) > 0 {
				excluded = make([]ResolvedSubject, 0, len(protoExcluded))
				for _, e := range protoExcluded {
					excluded = append(excluded, resolvedSubjectFromProto(e))
				}
			} else if ids := resp.GetExcludedSubjectIds(); len(ids) > 0 { //nolint:staticcheck
				// Fall back to the deprecated excluded_subject_ids field, which
				// carries only IDs (no permissionship/caveat info).
				excluded = make([]ResolvedSubject, 0, len(ids))
				for _, id := range ids {
					excluded = append(excluded, ResolvedSubject{SubjectID: id})
				}
			}

			result := LookupSubject{
				Subject:          subject,
				ExcludedSubjects: excluded,
				LookedUpAt:       resp.GetLookedUpAt().GetToken(),
			}
			if !yield(result, nil) {
				return
			}
		}
	}
}
