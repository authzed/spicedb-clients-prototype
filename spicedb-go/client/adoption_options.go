package client

import (
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
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
