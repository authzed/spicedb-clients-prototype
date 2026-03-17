package client

import (
	"context"
	"fmt"
	"io"
	"iter"

	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
	"github.com/authzed/spicedb-clients/spicedb-go/consistency"
)

const defaultLookupPageSize = 512

// LookupResources returns an iterator over resource IDs of the given type that
// the subject has the specified permission on. Cursors are handled
// transparently — the client automatically re-fetches pages of 512 results.
func (c *Client) LookupResources(ctx context.Context, cs consistency.Strategy, resourceType, permission, subjectType, subjectID string) iter.Seq2[string, error] {
	return func(yield func(string, error) bool) {
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
				yield("", fmt.Errorf("spicedb: lookup resources: %w", err))
				return
			}

			var count int
			for {
				resp, err := stream.Recv()
				if err == io.EOF {
					break
				}
				if err != nil {
					yield("", fmt.Errorf("spicedb: lookup resources: %w", err))
					return
				}
				count++
				cursor = resp.GetAfterResultCursor()
				if !yield(resp.GetResourceObjectId(), nil) {
					return
				}
			}

			if count < defaultLookupPageSize {
				return
			}
		}
	}
}

// LookupSubjects returns an iterator over subject IDs of the given type that
// have the specified permission on the resource. Unlike LookupResources,
// LookupSubjects does not currently support cursor-based pagination in SpiceDB
// and streams all results in a single server-streaming call.
func (c *Client) LookupSubjects(ctx context.Context, cs consistency.Strategy, resourceType, resourceID, permission, subjectType string) iter.Seq2[string, error] {
	return func(yield func(string, error) bool) {
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
			yield("", fmt.Errorf("spicedb: lookup subjects: %w", err))
			return
		}

		for {
			resp, err := stream.Recv()
			if err == io.EOF {
				return
			}
			if err != nil {
				yield("", fmt.Errorf("spicedb: lookup subjects: %w", err))
				return
			}
			subjectID := resp.GetSubject().GetSubjectObjectId()
			if subjectID == "" {
				// Fall back to deprecated field
				subjectID = resp.GetSubjectObjectId() //nolint:staticcheck
			}
			if !yield(subjectID, nil) {
				return
			}
		}
	}
}
