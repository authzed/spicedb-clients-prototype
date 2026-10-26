package client

import (
	"context"
	"fmt"
	"iter"

	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
	"github.com/authzed/spicedb-clients/spicedb-go/consistency"
	"github.com/authzed/spicedb-clients/spicedb-go/rel"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

const defaultCheckBatchSize = 1000

// PermissionCheck is a mutable request. Check snapshots nested context at entry.
type PermissionCheck struct {
	Resource    rel.ObjectRef
	Permission  string
	Subject     rel.SubjectRef
	Context     map[string]any
	Consistency consistency.Strategy
}

// BulkCheckError identifies the original global input index and preserves the typed cause.
type BulkCheckError struct {
	Index int
	Err   error
}

func (e *BulkCheckError) Error() string {
	return fmt.Sprintf("spicedb: check item %d: %v", e.Index, e.Err)
}
func (e *BulkCheckError) Unwrap() error { return e.Err }
func checkItem(r PermissionCheck, defaults map[string]any) (*v1.CheckBulkPermissionsRequestItem, error) {
	if e := r.Resource.Validate(); e != nil {
		return nil, invalid("resource: %s", e)
	}
	if e := r.Subject.Validate(); e != nil {
		return nil, invalid("subject: %s", e)
	}
	if r.Permission == "" {
		return nil, invalid("permission is required")
	}
	if _, e := strategy(r.Consistency); e != nil {
		return nil, e
	}
	ctx, e := contextStruct(mergeCheckContext(defaults, r.Context))
	if e != nil {
		return nil, e
	}
	return &v1.CheckBulkPermissionsRequestItem{Resource: object(r.Resource), Permission: r.Permission, Subject: subject(r.Subject), Context: ctx}, nil
}
func mergeCheckContext(defaults, item map[string]any) map[string]any {
	if len(defaults) == 0 && len(item) == 0 {
		return nil
	}
	m := make(map[string]any, len(defaults)+len(item))
	for k, v := range defaults {
		m[k] = v
	}
	for k, v := range item {
		m[k] = v
	}
	return m
}

// Check uses the singular CheckPermission RPC, including its native tracing flag.
func (c *Client) Check(ctx context.Context, request PermissionCheck, opts ...CheckOption) (CheckResult, error) {
	o := operationOptions{}
	for _, opt := range opts {
		opt(&o)
	}
	ctx, cancel, e := c.operation(ctx, o, false)
	if e != nil {
		return CheckResult{}, e
	}
	defer cancel()
	item, e := checkItem(request, o.checkContext)
	if e != nil {
		return CheckResult{}, e
	}
	cs, _ := strategy(request.Consistency)
	resp, e := retry(ctx, "check", func() (*v1.CheckPermissionResponse, error) {
		return c.psc.CheckPermission(ctx, &v1.CheckPermissionRequest{Consistency: cs, Resource: item.Resource, Permission: item.Permission, Subject: item.Subject, Context: item.Context, WithTracing: o.tracing})
	})
	if e != nil {
		return CheckResult{}, e
	}
	return checkResultFromProto(resp), nil
}
func (c *Client) CheckBulk(ctx context.Context, requests []PermissionCheck, opts ...CheckOption) ([]CheckResult, error) {
	o := operationOptions{}
	for _, opt := range opts {
		opt(&o)
	}
	ctx, cancel, e := c.operation(ctx, o, false)
	if e != nil {
		return nil, e
	}
	defer cancel()
	items := make([]*v1.CheckBulkPermissionsRequestItem, len(requests))
	for i, r := range requests {
		if i > 0 && r.Consistency != requests[0].Consistency {
			return nil, &BulkCheckError{i, invalid("mixed consistency strategies or revision tokens")}
		}
		items[i], e = checkItem(r, o.checkContext)
		if e != nil {
			return nil, &BulkCheckError{i, e}
		}
	}
	out := make([]CheckResult, 0, len(items))
	if len(items) == 0 {
		return out, nil
	}
	cs, _ := strategy(requests[0].Consistency)
	for start := 0; start < len(items); start += defaultCheckBatchSize {
		end := min(start+defaultCheckBatchSize, len(items))
		results, e := c.checkChunk(ctx, cs, items[start:end], start, o.tracing)
		if e != nil {
			return nil, e
		}
		out = append(out, results...)
	}
	return out, nil
}
func (c *Client) checkChunk(ctx context.Context, cs *v1.Consistency, items []*v1.CheckBulkPermissionsRequestItem, offset int, tracing bool) ([]CheckResult, error) {
	resp, e := retry(ctx, "check bulk", func() (*v1.CheckBulkPermissionsResponse, error) {
		return c.psc.CheckBulkPermissions(ctx, &v1.CheckBulkPermissionsRequest{Consistency: cs, Items: items, WithTracing: tracing})
	})
	if e != nil {
		return nil, e
	}
	if len(resp.GetPairs()) != len(items) {
		return nil, &Error{Code: CodeInternal, Message: fmt.Sprintf("spicedb: check bulk returned %d pairs for %d items at index %d", len(resp.GetPairs()), len(items), offset)}
	}
	out := make([]CheckResult, len(items))
	for i, pair := range resp.GetPairs() {
		if pair.GetRequest() == nil || !proto.Equal(pair.GetRequest(), items[i]) {
			return nil, &BulkCheckError{offset + i, &Error{Code: CodeInternal, Message: "spicedb: missing, duplicate or misordered response request"}}
		}
		if perr := pair.GetError(); perr != nil {
			return nil, &BulkCheckError{offset + i, mapGRPCError("check bulk", status.FromProto(perr).Err())}
		}
		if pair.GetItem() == nil {
			return nil, &BulkCheckError{offset + i, &Error{Code: CodeInternal, Message: "spicedb: response pair has neither result nor error"}}
		}
		out[i] = checkResultFromBulkItem(pair.GetItem(), resp.GetCheckedAt().GetToken())
	}
	return out, nil
}

// CheckIter consumes input with backpressure. Its optional timeout starts at iteration.
func (c *Client) CheckIter(ctx context.Context, requests iter.Seq[PermissionCheck], opts ...CheckOption) iter.Seq2[CheckResult, error] {
	o := operationOptions{}
	for _, opt := range opts {
		opt(&o)
	}
	return func(yield func(CheckResult, error) bool) {
		ctx, cancel, e := c.operation(ctx, o, true)
		if e != nil {
			yield(CheckResult{}, e)
			return
		}
		defer cancel()
		if requests == nil {
			yield(CheckResult{}, invalid("requests iterator required"))
			return
		}
		var cs consistency.Strategy
		var pcs *v1.Consistency
		index := 0
		items := make([]*v1.CheckBulkPermissionsRequestItem, 0, defaultCheckBatchSize)
		flush := func() bool {
			if len(items) == 0 {
				return true
			}
			out, e := c.checkChunk(ctx, pcs, items, index-len(items), o.tracing)
			items = items[:0]
			if e != nil {
				yield(CheckResult{}, e)
				return false
			}
			for _, r := range out {
				if !yield(r, nil) {
					return false
				}
			}
			return true
		}
		for r := range requests {
			if e := ctx.Err(); e != nil {
				yield(CheckResult{}, mapGRPCError("check iter", e))
				return
			}
			if index == 0 {
				cs = r.Consistency
				pcs, e = strategy(cs)
				if e != nil {
					yield(CheckResult{}, &BulkCheckError{index, e})
					return
				}
			} else if r.Consistency != cs {
				yield(CheckResult{}, &BulkCheckError{index, invalid("mixed consistency strategies or revision tokens")})
				return
			}
			item, e := checkItem(r, o.checkContext)
			if e != nil {
				yield(CheckResult{}, &BulkCheckError{index, e})
				return
			}
			items = append(items, item)
			index++
			if len(items) == defaultCheckBatchSize && !flush() {
				return
			}
		}
		flush()
	}
}
