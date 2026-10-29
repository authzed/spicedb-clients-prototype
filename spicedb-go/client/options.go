package client

import (
	"context"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/authzed/spicedb-clients/spicedb-go/consistency"
	"github.com/authzed/spicedb-clients/spicedb-go/internal/native"
	"github.com/authzed/spicedb-clients/spicedb-go/rel"
	"google.golang.org/grpc"
)

type operationOptions struct {
	lookupPermissionshipFilter  LookupPermissionshipFilter
	permissionSetCursor         *PermissionSetCursor
	schemaFilters               []SchemaFilter
	definitionFilter            string
	metadata                    map[string]any
	timeout                     time.Duration
	pageSize                    uint32
	limit                       uint64
	cursor                      string
	checkContext                map[string]any
	tracing, debug, checkpoints bool
	start                       consistency.ZedToken
	preconditions               []rel.Precondition
	filter                      *rel.Filter
	subjectRelation             string
	callOptions                 []grpc.CallOption
	callRetryPolicy             *RetryPolicy
	opRetryPolicy               *RetryPolicy
	itemErrors                  bool
	schemaChanges               bool
	disallowPartialDeletion     bool
	deleteProgress              func(DeleteResult)
	singleRequest               bool
	err                         error
}

func invalid(format string, args ...any) error {
	return &Error{Code: CodeInvalidArgument, Message: fmt.Sprintf("spicedb: "+format, args...)}
}
func timeoutOption(o *operationOptions, d time.Duration) {
	if d <= 0 {
		o.err = invalid("timeout must be positive")
		return
	}
	o.timeout = d
}
func (c *Client) operation(ctx context.Context, o operationOptions, stream bool) (context.Context, context.CancelFunc, error) {
	if o.err != nil {
		return nil, nil, o.err
	}
	if e := ctx.Err(); e != nil {
		return nil, nil, mapGRPCError("operation", e)
	}
	d := o.timeout
	if d == 0 && !stream && !c.noDefaultDeadline {
		d = c.defaultTimeout
		if d == 0 {
			d = 30 * time.Second
		}
	}
	if d > 0 {
		x, cancel := context.WithTimeout(ctx, d)
		return x, cancel, nil
	}
	x, cancel := context.WithCancel(ctx)
	return x, cancel, nil
}

// pageContext bounds one page request of a finite paged read (relationship
// reads, lookups, exports) by the client default deadline, from opening the
// page to receiving its last item, so a server that stalls mid-read fails the
// read instead of hanging it. ctx carries the operation's total deadline,
// which still wins when earlier. WithDefaultDeadline(DefaultDeadlineDisabled)
// removes the per-page default; watches never call this.
func (c *Client) pageContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if c.noDefaultDeadline {
		return context.WithCancel(ctx)
	}
	d := c.defaultTimeout
	if d == 0 {
		d = 30 * time.Second
	}
	return context.WithTimeout(ctx, d)
}

// retry runs an idempotent read under the client's retry policy.
func retry[T any](ctx context.Context, rs retrySettings, op string, call func() (T, error)) (T, error) {
	var zero T
	policy := rs.policy
	for attempt := 0; ; attempt++ {
		if e := ctx.Err(); e != nil {
			return zero, mapGRPCError(op, e)
		}
		r, e := call()
		if e == nil {
			return r, nil
		}
		if attempt >= policy.MaxRetries || !policy.retryable(e) {
			return zero, mapGRPCError(op, e)
		}
		if e := policy.backoff(ctx, op, attempt, e); e != nil {
			return zero, mapGRPCError(op, e)
		}
	}
}

// backoff reports the retry and sleeps a full-jitter delay, returning early
// with the context's error if it ends first.
func (p RetryPolicy) backoff(ctx context.Context, op string, attempt int, cause error) error {
	delay := time.Duration(rand.Int64N(int64(p.delayBound(attempt))))
	if p.OnRetry != nil {
		p.OnRetry(RetryEvent{Operation: op, Attempt: attempt + 1, Err: mapGRPCError(op, cause), Delay: delay})
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
func snapshotContext(ctx map[string]any) (map[string]any, error) {
	m, e := native.Map(ctx)
	if e != nil {
		return nil, invalid("context %s", e)
	}
	return m, nil
}

type CheckOption func(*operationOptions)

func WithCheckTimeout(d time.Duration) CheckOption {
	return func(o *operationOptions) { timeoutOption(o, d) }
}

type WriteOption func(*operationOptions)

func WithWriteTimeout(d time.Duration) WriteOption {
	return func(o *operationOptions) { timeoutOption(o, d) }
}

type ReadRelationshipsOption func(*operationOptions)

func WithReadRelationshipsTimeout(d time.Duration) ReadRelationshipsOption {
	return func(o *operationOptions) { timeoutOption(o, d) }
}

// LookupPermissionshipFilter selects which LookupResources results are yielded.
type LookupPermissionshipFilter uint8

const (
	// LookupAll preserves all results, including conditional and unknown permissionships.
	LookupAll LookupPermissionshipFilter = iota
	// LookupHasPermissionOnly yields only unconditional grants.
	LookupHasPermissionOnly
)

type LookupResourcesOption func(*operationOptions)

// WithLookupResourcesPermissionshipFilter filters results client-side. Limits count yielded results.
func WithLookupResourcesPermissionshipFilter(mode LookupPermissionshipFilter) LookupResourcesOption {
	return func(o *operationOptions) {
		if mode != LookupAll && mode != LookupHasPermissionOnly {
			o.err = invalid("unknown lookup permissionship filter")
			return
		}
		o.lookupPermissionshipFilter = mode
	}
}

func WithLookupResourcesTimeout(d time.Duration) LookupResourcesOption {
	return func(o *operationOptions) { timeoutOption(o, d) }
}

type LookupSubjectsOption func(*operationOptions)

func WithLookupSubjectsTimeout(d time.Duration) LookupSubjectsOption {
	return func(o *operationOptions) { timeoutOption(o, d) }
}

type ReadSchemaOption func(*operationOptions)

func WithReadSchemaTimeout(d time.Duration) ReadSchemaOption {
	return func(o *operationOptions) { timeoutOption(o, d) }
}

type WriteSchemaOption func(*operationOptions)

func WithWriteSchemaTimeout(d time.Duration) WriteSchemaOption {
	return func(o *operationOptions) { timeoutOption(o, d) }
}

type ReflectSchemaOption func(*operationOptions)

func WithReflectSchemaTimeout(d time.Duration) ReflectSchemaOption {
	return func(o *operationOptions) { timeoutOption(o, d) }
}

type ComputablePermissionsOption func(*operationOptions)

func WithComputablePermissionsTimeout(d time.Duration) ComputablePermissionsOption {
	return func(o *operationOptions) { timeoutOption(o, d) }
}

type DependentRelationsOption func(*operationOptions)

func WithDependentRelationsTimeout(d time.Duration) DependentRelationsOption {
	return func(o *operationOptions) { timeoutOption(o, d) }
}

type DiffSchemaOption func(*operationOptions)

func WithDiffSchemaTimeout(d time.Duration) DiffSchemaOption {
	return func(o *operationOptions) { timeoutOption(o, d) }
}

type ExpandPermissionTreeOption func(*operationOptions)

func WithExpandPermissionTreeTimeout(d time.Duration) ExpandPermissionTreeOption {
	return func(o *operationOptions) { timeoutOption(o, d) }
}

type ImportRelationshipsOption func(*operationOptions)

func WithImportRelationshipsTimeout(d time.Duration) ImportRelationshipsOption {
	return func(o *operationOptions) { timeoutOption(o, d) }
}

type ExportRelationshipsOption func(*operationOptions)

func WithExportRelationshipsTimeout(d time.Duration) ExportRelationshipsOption {
	return func(o *operationOptions) { timeoutOption(o, d) }
}

type UpdatesOption func(*operationOptions)

func WithUpdatesTimeout(d time.Duration) UpdatesOption {
	return func(o *operationOptions) { timeoutOption(o, d) }
}

type RegisterRelationshipCounterOption func(*operationOptions)

func WithRegisterRelationshipCounterTimeout(d time.Duration) RegisterRelationshipCounterOption {
	return func(o *operationOptions) { timeoutOption(o, d) }
}

type CountRelationshipsOption func(*operationOptions)

func WithCountRelationshipsTimeout(d time.Duration) CountRelationshipsOption {
	return func(o *operationOptions) { timeoutOption(o, d) }
}

type UnregisterRelationshipCounterOption func(*operationOptions)

func WithUnregisterRelationshipCounterTimeout(d time.Duration) UnregisterRelationshipCounterOption {
	return func(o *operationOptions) { timeoutOption(o, d) }
}

type RoaringLookupResourcesOption func(*operationOptions)

func WithRoaringLookupResourcesTimeout(d time.Duration) RoaringLookupResourcesOption {
	return func(o *operationOptions) { timeoutOption(o, d) }
}

type DeleteOption func(*operationOptions)

func WithDeleteTimeout(d time.Duration) DeleteOption {
	return func(o *operationOptions) { timeoutOption(o, d) }
}

type DeleteAllOption func(*operationOptions)

func WithDeleteAllTimeout(d time.Duration) DeleteAllOption {
	return func(o *operationOptions) { timeoutOption(o, d) }
}
func WithReadRelationshipsPageSize(n uint32) ReadRelationshipsOption {
	return func(o *operationOptions) {
		if n == 0 {
			o.err = invalid("page size must be positive")
			return
		}
		o.pageSize = n
	}
}
func WithLookupResourcesPageSize(n uint32) LookupResourcesOption {
	return func(o *operationOptions) {
		if n == 0 {
			o.err = invalid("page size must be positive")
			return
		}
		o.pageSize = n
	}
}
func WithExportRelationshipsPageSize(n uint32) ExportRelationshipsOption {
	return func(o *operationOptions) {
		if n == 0 {
			o.err = invalid("page size must be positive")
			return
		}
		o.pageSize = n
	}
}
func WithReadRelationshipsLimit(n uint64) ReadRelationshipsOption {
	return func(o *operationOptions) {
		if n == 0 {
			o.err = invalid("result limit must be positive")
			return
		}
		o.limit = n
	}
}
func WithLookupResourcesLimit(n uint64) LookupResourcesOption {
	return func(o *operationOptions) {
		if n == 0 {
			o.err = invalid("result limit must be positive")
			return
		}
		o.limit = n
	}
}
func WithLookupSubjectsLimit(n uint64) LookupSubjectsOption {
	return func(o *operationOptions) {
		if n == 0 {
			o.err = invalid("result limit must be positive")
			return
		}
		o.limit = n
	}
}
func WithExportRelationshipsLimit(n uint64) ExportRelationshipsOption {
	return func(o *operationOptions) {
		if n == 0 {
			o.err = invalid("result limit must be positive")
			return
		}
		o.limit = n
	}
}
func WithCheckContext(m map[string]any) CheckOption {
	snapshot, e := snapshotContext(m)
	return func(o *operationOptions) {
		o.checkContext = snapshot
		if e != nil {
			o.err = e
		}
	}
}
func WithLookupResourcesContext(m map[string]any) LookupResourcesOption {
	snapshot, e := snapshotContext(m)
	return func(o *operationOptions) {
		o.checkContext = snapshot
		if e != nil {
			o.err = e
		}
	}
}
func WithLookupSubjectsContext(m map[string]any) LookupSubjectsOption {
	snapshot, e := snapshotContext(m)
	return func(o *operationOptions) {
		o.checkContext = snapshot
		if e != nil {
			o.err = e
		}
	}
}

// CheckTracing selects the operation's tracing behavior.
type CheckTracing uint8

const (
	TracingDisabled CheckTracing = iota
	TracingEnabled
)

func WithCheckTracing(mode CheckTracing) CheckOption {
	return func(o *operationOptions) {
		if mode != TracingDisabled && mode != TracingEnabled {
			o.err = invalid("unknown tracing mode")
			return
		}
		o.tracing = mode == TracingEnabled
	}
}

// LookupDebug selects the operation's debug behavior.
type LookupDebug uint8

const (
	DebugDisabled LookupDebug = iota
	DebugEnabled
)

func WithLookupResourcesDebug(mode LookupDebug) LookupResourcesOption {
	return func(o *operationOptions) {
		if mode != DebugDisabled && mode != DebugEnabled {
			o.err = invalid("unknown debug mode")
			return
		}
		o.debug = mode == DebugEnabled
	}
}
func WithLookupSubjectsRelation(relation string) LookupSubjectsOption {
	return func(o *operationOptions) {
		if relation == "..." {
			o.err = invalid("use empty subject relation for direct subjects")
			return
		}
		o.subjectRelation = relation
	}
}
func WithExportRelationshipsFilter(f rel.Filter) ExportRelationshipsOption {
	return func(o *operationOptions) { o.filter = &f }
}
func WithUpdatesStart(token consistency.ZedToken) UpdatesOption {
	return func(o *operationOptions) { o.start = token }
}

// WatchCheckpoints selects the operation's checkpoints behavior.
type WatchCheckpoints uint8

const (
	CheckpointsExcluded WatchCheckpoints = iota
	CheckpointsIncluded
)

func WithUpdatesCheckpoints(mode WatchCheckpoints) UpdatesOption {
	return func(o *operationOptions) {
		if mode != CheckpointsExcluded && mode != CheckpointsIncluded {
			o.err = invalid("unknown checkpoints mode")
			return
		}
		o.checkpoints = mode == CheckpointsIncluded
	}
}
func WithDeleteAllPageSize(n uint32) DeleteAllOption {
	return func(o *operationOptions) {
		if n == 0 {
			o.err = invalid("page size must be positive")
			return
		}
		o.pageSize = n
	}
}
func WithDeleteCursor(cursor string) DeleteOption {
	return func(o *operationOptions) { o.cursor = cursor }
}
func WithDeleteMustMatch(f rel.Filter) DeleteOption {
	return func(o *operationOptions) {
		if e := f.Validate(); e != nil {
			o.err = invalid("precondition: %s", e)
			return
		}
		o.preconditions = append(o.preconditions, rel.Precondition{Filter: f, Operation: rel.PreconditionMustMatch})
	}
}
func WithDeleteMustNotMatch(f rel.Filter) DeleteOption {
	return func(o *operationOptions) {
		if e := f.Validate(); e != nil {
			o.err = invalid("precondition: %s", e)
			return
		}
		o.preconditions = append(o.preconditions, rel.Precondition{Filter: f, Operation: rel.PreconditionMustNotMatch})
	}
}
func WithDeleteAllCursor(cursor string) DeleteAllOption {
	return func(o *operationOptions) { o.cursor = cursor }
}
func WithDeleteAllMustMatch(f rel.Filter) DeleteAllOption {
	return func(o *operationOptions) {
		if e := f.Validate(); e != nil {
			o.err = invalid("precondition: %s", e)
			return
		}
		o.preconditions = append(o.preconditions, rel.Precondition{Filter: f, Operation: rel.PreconditionMustMatch})
	}
}
func WithDeleteAllMustNotMatch(f rel.Filter) DeleteAllOption {
	return func(o *operationOptions) {
		if e := f.Validate(); e != nil {
			o.err = invalid("precondition: %s", e)
			return
		}
		o.preconditions = append(o.preconditions, rel.Precondition{Filter: f, Operation: rel.PreconditionMustNotMatch})
	}
}

func WithWriteTransactionMetadata(metadata map[string]any) WriteOption {
	snapshot, e := snapshotContext(metadata)
	return func(o *operationOptions) {
		o.metadata = snapshot
		if e != nil {
			o.err = e
		}
	}
}
