package client

import (
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"github.com/authzed/spicedb-clients/spicedb-go/consistency"
	"github.com/authzed/spicedb-clients/spicedb-go/rel"
)

// operationOption is satisfied by every operation's option type
// (CheckOption, WriteOption, ReadSchemaOption, ...).
type operationOption interface {
	~func(*operationOptions)
}

// WithCallOptions passes gRPC call options to every RPC the operation makes.
// It is the supported escape hatch for transport-level behavior, available on
// every operation; instantiate it with the operation's option type:
//
//	c.ReadSchema(ctx, req, client.WithCallOptions[client.ReadSchemaOption](grpc.Header(&md)))
//
// Options apply to each attempt, chunk and page. Call options that change
// retry or transport security are the caller's responsibility.
func WithCallOptions[O operationOption](opts ...grpc.CallOption) O {
	opts = append([]grpc.CallOption(nil), opts...)
	return func(o *operationOptions) { o.callOptions = append(o.callOptions, opts...) }
}

// WithResponseMetadata captures the response headers and trailers of the
// operation's RPC into header and trailer (either may be nil). For operations
// that make several RPCs (retries, chunks, pages), the last RPC's metadata
// wins. Data that SpiceDB versions before 1.30 send in trailers is reachable
// this way.
func WithResponseMetadata[O operationOption](header, trailer *metadata.MD) O {
	var opts []grpc.CallOption
	if header != nil {
		opts = append(opts, grpc.Header(header))
	}
	if trailer != nil {
		opts = append(opts, grpc.Trailer(trailer))
	}
	return WithCallOptions[O](opts...)
}

// BulkItemErrors selects how CheckBulk and CheckIter report per-item failures.
type BulkItemErrors uint8

const (
	// ItemErrorsFail fails the whole call with a *BulkCheckError for the first
	// failing item. This is the default.
	ItemErrorsFail BulkItemErrors = iota
	// ItemErrorsReturned returns every item's result; a failed item's result
	// has Err set (a *BulkCheckError) and PermissionshipUnspecified, so it
	// never grants.
	ItemErrorsReturned
)

// WithBulkItemErrors selects how per-item failures are reported.
func WithBulkItemErrors(mode BulkItemErrors) CheckOption {
	return func(o *operationOptions) {
		if mode != ItemErrorsFail && mode != ItemErrorsReturned {
			o.err = invalid("unknown bulk item error mode")
			return
		}
		o.itemErrors = mode == ItemErrorsReturned
	}
}

// WatchSchemaChanges selects whether Updates requests schema-change events.
type WatchSchemaChanges uint8

const (
	SchemaChangesExcluded WatchSchemaChanges = iota
	SchemaChangesIncluded
)

// WithUpdatesSchemaChanges requests schema-change events, reported as
// WatchEvent.SchemaUpdated. It combines with WithUpdatesCheckpoints.
func WithUpdatesSchemaChanges(mode WatchSchemaChanges) UpdatesOption {
	return func(o *operationOptions) {
		if mode != SchemaChangesExcluded && mode != SchemaChangesIncluded {
			o.err = invalid("unknown schema change mode")
			return
		}
		o.schemaChanges = mode == SchemaChangesIncluded
	}
}

// PartialDeletion selects whether DeleteRelationships may delete only part of
// the matching relationships.
type PartialDeletion uint8

const (
	// PartialAllowed deletes up to Limit relationships and reports whether it
	// completed. This is the default, and Limit must be positive.
	PartialAllowed PartialDeletion = iota
	// PartialDisallowed is all-or-nothing: the server rejects a delete that
	// would exceed the limit instead of deleting part of it. Limit may be zero
	// to use the server's configured default limit.
	PartialDisallowed
)

// WithDeletePartial selects partial or all-or-nothing deletion.
func WithDeletePartial(mode PartialDeletion) DeleteOption {
	return func(o *operationOptions) {
		if mode != PartialAllowed && mode != PartialDisallowed {
			o.err = invalid("unknown partial deletion mode")
			return
		}
		o.disallowPartialDeletion = mode == PartialDisallowed
	}
}

// WithDeleteAllProgress calls progress after every acknowledged batch with the
// cumulative result so far. It runs on the calling goroutine and must not
// block for long, since the operation budget keeps running.
func WithDeleteAllProgress(progress func(DeleteResult)) DeleteAllOption {
	return func(o *operationOptions) { o.deleteProgress = progress }
}

// WithImportRelationshipsBatchSize sets the relationships sent per stream
// message (default 1,000). It does not split the import into several
// transactions.
func WithImportRelationshipsBatchSize(n uint32) ImportRelationshipsOption {
	return func(o *operationOptions) {
		if n == 0 {
			o.err = invalid("import batch size must be positive")
			return
		}
		o.pageSize = n
	}
}

// WithReadRelationshipsCursor resumes a read after the item whose
// AfterResultCursor is cursor. SpiceDB accepts a cursor only for an identical
// call: repeat the request and its page size.
func WithReadRelationshipsCursor(cursor string) ReadRelationshipsOption {
	return func(o *operationOptions) { o.cursor = cursor }
}

// WithExportRelationshipsCursor resumes an export after the item whose
// AfterResultCursor is cursor. Repeat the request and its page size.
func WithExportRelationshipsCursor(cursor string) ExportRelationshipsOption {
	return func(o *operationOptions) { o.cursor = cursor }
}

// WithLookupResourcesCursor resumes a lookup after the item whose
// AfterResultCursor is cursor. Repeat the request and its page size.
func WithLookupResourcesCursor(cursor string) LookupResourcesOption {
	return func(o *operationOptions) { o.cursor = cursor }
}

// DefaultDeadline selects whether unary operations get the client's default
// deadline when neither the context nor a per-call timeout sets one.
type DefaultDeadline uint8

const (
	// DefaultDeadlineEnabled applies the default (30 seconds, or
	// WithDefaultTimeout's value). This is the default.
	DefaultDeadlineEnabled DefaultDeadline = iota
	// DefaultDeadlineDisabled applies no client deadline. The caller's
	// context deadline and per-call timeouts still apply.
	DefaultDeadlineDisabled
)

// WithDefaultDeadline enables or disables the client's default deadline.
func WithDefaultDeadline(mode DefaultDeadline) Option {
	return func(o *options) {
		if mode != DefaultDeadlineEnabled && mode != DefaultDeadlineDisabled {
			o.err = invalid("unknown default deadline mode")
			return
		}
		o.noDefaultDeadline = mode == DefaultDeadlineDisabled
	}
}

// RetryEvent describes a retry about to happen, for RetryPolicy.OnRetry.
type RetryEvent struct {
	// Operation names the operation, e.g. "read schema".
	Operation string
	// Attempt is the attempt that failed, starting at 1.
	Attempt int
	// Err is the attempt's error.
	Err error
	// Delay is the backoff before the next attempt.
	Delay time.Duration
}

// RetryPolicy configures retries of idempotent reads. Mutations (writes,
// deletes, imports, counter registration) are never retried, whatever the
// policy says.
type RetryPolicy struct {
	// MaxRetries is the number of retries after the first attempt. Zero
	// disables retries.
	MaxRetries int
	// RetryableCodes lists the error codes that are retried.
	RetryableCodes []ErrorCode
	// InitialBackoff is the upper bound of the first full-jitter delay; each
	// retry doubles it. Must be positive when MaxRetries is positive.
	InitialBackoff time.Duration
	// MaxBackoff caps the delay bound. Zero means uncapped.
	MaxBackoff time.Duration
	// OnRetry, if set, is called before each backoff, e.g. to log retries.
	OnRetry func(RetryEvent)
}

// DefaultRetryPolicy returns the built-in policy: three retries of
// Unavailable and Aborted with full-jitter backoff starting at 100ms.
func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{MaxRetries: 3, RetryableCodes: []ErrorCode{CodeUnavailable, CodeAborted}, InitialBackoff: 100 * time.Millisecond}
}

// Retries enables or disables the client's retries of idempotent reads.
type Retries uint8

const (
	// RetriesEnabled uses the configured policy (DefaultRetryPolicy unless
	// WithRetryPolicy sets one). This is the default.
	RetriesEnabled Retries = iota
	// RetriesDisabled makes every operation a single attempt, for callers
	// that apply their own retry policy.
	RetriesDisabled
)

// WithRetries enables or disables retries.
func WithRetries(mode Retries) Option {
	return func(o *options) {
		if mode != RetriesEnabled && mode != RetriesDisabled {
			o.err = invalid("unknown retry mode")
			return
		}
		o.retriesDisabled = mode == RetriesDisabled
	}
}

// WithRetryPolicy replaces the retry policy for idempotent reads.
func WithRetryPolicy(policy RetryPolicy) Option {
	policy.RetryableCodes = append([]ErrorCode(nil), policy.RetryableCodes...)
	return func(o *options) {
		if policy.MaxRetries < 0 || (policy.MaxRetries > 0 && policy.InitialBackoff <= 0) || policy.MaxBackoff < 0 {
			o.err = invalid("retry policy needs non-negative retries and a positive initial backoff")
			return
		}
		o.retryPolicy = &policy
	}
}

// StreamRetries selects whether opening a server stream (reads, lookups,
// exports, watch) is retried before any item is exposed. A partially consumed
// stream is never restarted.
type StreamRetries uint8

const (
	// StreamRetriesEnabled retries stream establishment with the retry
	// policy. This is the default.
	StreamRetriesEnabled StreamRetries = iota
	// StreamRetriesDisabled opens each stream once, e.g. so a watch that the
	// caller supervises is never reopened by the client.
	StreamRetriesDisabled
)

// WithStreamRetries enables or disables stream-establishment retries.
func WithStreamRetries(mode StreamRetries) Option {
	return func(o *options) {
		if mode != StreamRetriesEnabled && mode != StreamRetriesDisabled {
			o.err = invalid("unknown stream retry mode")
			return
		}
		o.streamRetriesDisabled = mode == StreamRetriesDisabled
	}
}

// retryPolicy returns the effective policy for idempotent reads.
func (c *Client) retryPolicy() RetryPolicy {
	if c == nil || c.retries == nil {
		return DefaultRetryPolicy()
	}
	return *c.retries
}

func (p RetryPolicy) retryable(err error) bool {
	code := CodeOf(err)
	for _, c := range p.RetryableCodes {
		if c == code {
			return true
		}
	}
	return false
}

func (p RetryPolicy) delayBound(attempt int) time.Duration {
	d := p.InitialBackoff << attempt
	if d <= 0 || (p.MaxBackoff > 0 && d > p.MaxBackoff) {
		if p.MaxBackoff > 0 {
			return p.MaxBackoff
		}
		return p.InitialBackoff << 20
	}
	return d
}

// retrySettings is the retry behavior for one operation.
type retrySettings struct {
	policy  RetryPolicy
	streams bool // whether stream establishment may retry
}

// retrySettings resolves the client's retry behavior for one operation: a
// per-call WithCallRetryPolicy wins over the client-wide policy and also
// governs stream establishment for that call.
func (c *Client) retrySettings(o operationOptions) retrySettings {
	if o.callRetryPolicy != nil {
		return retrySettings{policy: *o.callRetryPolicy, streams: true}
	}
	return retrySettings{policy: c.retryPolicy(), streams: c == nil || !c.noStreamRetries}
}

// WithCallRetryPolicy overrides the client's retry policy for one call. Use
// RetryPolicy{} (zero retries) to exempt a single call or stream, such as a
// supervised Watch, from retries while the client retries everything else.
// For a stream it also governs reopening before the first item.
//
// Mutations are not retried by default. Two writes are idempotent on replay
// and may opt in through this option: a Write whose transaction contains only
// touches and no preconditions, and WriteSchema. Any other mutation given a
// policy with retries fails with ErrInvalidArgument before any RPC.
func WithCallRetryPolicy[O operationOption](policy RetryPolicy) O {
	policy.RetryableCodes = append([]ErrorCode(nil), policy.RetryableCodes...)
	return func(o *operationOptions) {
		if policy.MaxRetries < 0 || (policy.MaxRetries > 0 && policy.InitialBackoff <= 0) || policy.MaxBackoff < 0 {
			o.err = invalid("retry policy needs non-negative retries and a positive initial backoff")
			return
		}
		o.callRetryPolicy = &policy
	}
}

// mutationRetry returns the retry settings for a mutation: none unless the
// caller opted this call in, which only idempotent mutations may do.
func mutationRetry(o operationOptions, idempotent bool, op string) (retrySettings, error) {
	if o.callRetryPolicy == nil || o.callRetryPolicy.MaxRetries == 0 {
		return retrySettings{}, nil
	}
	if !idempotent {
		return retrySettings{}, invalid("%s cannot be retried: only touch-only writes without preconditions and schema writes are idempotent on replay", op)
	}
	return retrySettings{policy: *o.callRetryPolicy}, nil
}

// Paging selects how ReadRelationships and LookupResources fetch results.
type Paging uint8

const (
	// PagingAutomatic fetches page after page (page size from the page-size
	// option) until results or the limit run out. This is the default.
	PagingAutomatic Paging = iota
	// PagingSingleRequest sends exactly one request, as the raw API does: its
	// limit is the limit option (or unbounded without one), the page-size
	// option is ignored, and no follow-up page is requested. Each item still
	// carries its after-result cursor.
	PagingSingleRequest
)

func pagingOption(o *operationOptions, mode Paging) {
	if mode != PagingAutomatic && mode != PagingSingleRequest {
		o.err = invalid("unknown paging mode")
		return
	}
	o.singleRequest = mode == PagingSingleRequest
}

// WithReadRelationshipsPaging selects automatic paging or a single request.
func WithReadRelationshipsPaging(mode Paging) ReadRelationshipsOption {
	return func(o *operationOptions) { pagingOption(o, mode) }
}

// WithLookupResourcesPaging selects automatic paging or a single request.
func WithLookupResourcesPaging(mode Paging) LookupResourcesOption {
	return func(o *operationOptions) { pagingOption(o, mode) }
}

// pageRequestLimit is the OptionalLimit every request of a paged read sends:
// fixed for the whole call (SpiceDB rejects a cursor sent with a different
// limit), capped by the total limit, and the total limit alone (0 meaning
// unbounded) in single-request mode.
func (o operationOptions) pageRequestLimit() uint32 {
	if o.singleRequest {
		return uint32(min(o.limit, uint64(^uint32(0))))
	}
	if o.limit > 0 {
		return uint32(min(uint64(o.pageSize), o.limit))
	}
	return o.pageSize
}

// ResolvedOptions is what a set of operation options asks for, for test
// doubles and wrappers that need to observe per-call options (option values
// are otherwise opaque). It is a snapshot: changing it changes nothing.
type ResolvedOptions struct {
	// Err is the first invalid option, which the real client would return.
	Err     error
	Timeout time.Duration
	// PageSize is the page size; for ImportRelationships it is the batch
	// size and for DeleteAllRelationshipsNonAtomic the batch limit. Operation
	// defaults are applied.
	PageSize uint32
	// Limit is the total result limit; 0 means none.
	Limit                      uint64
	Cursor                     string
	Paging                     Paging
	CallOptions                []grpc.CallOption
	RetryPolicy                *RetryPolicy
	CheckContext               map[string]any
	Tracing                    CheckTracing
	BulkItemErrors             BulkItemErrors
	LookupDebug                LookupDebug
	LookupPermissionshipFilter LookupPermissionshipFilter
	LookupSubjectsRelation     string
	ExportFilter               *rel.Filter
	WatchStart                 consistency.ZedToken
	WatchCheckpoints           WatchCheckpoints
	WatchSchemaChanges         WatchSchemaChanges
	Preconditions              []rel.Precondition
	PartialDeletion            PartialDeletion
	DeleteProgress             func(DeleteResult)
	TransactionMetadata        map[string]any
}

// ResponseMetadata returns the header and trailer targets requested with
// WithResponseMetadata (nil when absent), so a test double can fill them.
func (r ResolvedOptions) ResponseMetadata() (header, trailer *metadata.MD) {
	for _, opt := range r.CallOptions {
		switch o := opt.(type) {
		case grpc.HeaderCallOption:
			header = o.HeaderAddr
		case grpc.TrailerCallOption:
			trailer = o.TrailerAddr
		}
	}
	return header, trailer
}

// ResolveOptions applies options of one operation's option type and reports
// what they ask for, with that operation's defaults applied:
//
//	r := client.ResolveOptions(opts...) // opts []client.ReadRelationshipsOption
//	if r.Cursor != "" { ... }
func ResolveOptions[O operationOption](opts ...O) ResolvedOptions {
	var o operationOptions
	switch any(opts).(type) {
	case []ReadRelationshipsOption:
		o.pageSize = defaultReadPageSize
	case []LookupResourcesOption:
		o.pageSize = defaultLookupPageSize
	case []ExportRelationshipsOption:
		o.pageSize = defaultExportPageSize
	case []ImportRelationshipsOption:
		o.pageSize = defaultImportBatchSize
	case []DeleteAllOption:
		o.pageSize = defaultDeletePageSize
	}
	for _, opt := range opts {
		opt(&o)
	}
	r := ResolvedOptions{
		Err: o.err, Timeout: o.timeout, PageSize: o.pageSize, Limit: o.limit, Cursor: o.cursor,
		CallOptions: append([]grpc.CallOption(nil), o.callOptions...), CheckContext: o.checkContext,
		LookupPermissionshipFilter: o.lookupPermissionshipFilter, LookupSubjectsRelation: o.subjectRelation,
		WatchStart: o.start, Preconditions: append([]rel.Precondition(nil), o.preconditions...),
		DeleteProgress: o.deleteProgress, TransactionMetadata: o.metadata,
	}
	if o.callRetryPolicy != nil {
		p := *o.callRetryPolicy
		r.RetryPolicy = &p
	}
	if o.filter != nil {
		f := *o.filter
		r.ExportFilter = &f
	}
	if o.singleRequest {
		r.Paging = PagingSingleRequest
	}
	if o.tracing {
		r.Tracing = TracingEnabled
	}
	if o.itemErrors {
		r.BulkItemErrors = ItemErrorsReturned
	}
	if o.debug {
		r.LookupDebug = DebugEnabled
	}
	if o.checkpoints {
		r.WatchCheckpoints = CheckpointsIncluded
	}
	if o.schemaChanges {
		r.WatchSchemaChanges = SchemaChangesIncluded
	}
	if o.disallowPartialDeletion {
		r.PartialDeletion = PartialDisallowed
	}
	return r
}
