package client

import (
	"errors"
	"fmt"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Sentinel errors for gRPC conditions, matchable with errors.Is.
var (
	ErrNotFound           = errors.New("spicedb: not found")
	ErrAlreadyExists      = errors.New("spicedb: already exists")
	ErrInvalidArgument    = errors.New("spicedb: invalid argument")
	ErrFailedPrecondition = errors.New("spicedb: failed precondition")
	ErrPermissionDenied   = errors.New("spicedb: permission denied")
	ErrUnauthenticated    = errors.New("spicedb: unauthenticated")
	ErrUnavailable        = errors.New("spicedb: unavailable")
	ErrCanceled           = errors.New("spicedb: canceled")
	ErrDeadlineExceeded   = errors.New("spicedb: deadline exceeded")
	ErrResourceExhausted  = errors.New("spicedb: resource exhausted")

	// ErrOutOfRange is SpiceDB's signal that a ZedToken is no longer usable:
	// the revision it names has expired or been garbage-collected. Recovery is
	// mechanical -- discard the stale token and re-read at full consistency --
	// which is why it is a sentinel of its own rather than a generic failure a
	// caller has to recognize by its message.
	ErrOutOfRange = errors.New("spicedb: out of range")
)

// ErrorCode is a native, gRPC-independent classification of a SpiceDB error.
// It exists so the primary error API (Error.Code) never exposes a raw
// google.golang.org/grpc/codes.Code value to callers; use errors.Unwrap plus
// google.golang.org/grpc/status for advanced inspection of the underlying
// gRPC status, if needed.
type ErrorCode int

const (
	// CodeUnknown is the fallback for any gRPC code without a more specific
	// native mapping (including codes not defined at the time this client was
	// built).
	CodeUnknown ErrorCode = iota
	CodeNotFound
	CodeAlreadyExists
	CodeInvalidArgument
	CodeFailedPrecondition
	CodePermissionDenied
	CodeUnauthenticated
	CodeUnavailable
	CodeResourceExhausted
	CodeAborted
	CodeDeadlineExceeded
	CodeCanceled
	CodeInternal
	// CodeOutOfRange is an expired or garbage-collected ZedToken. New
	// ErrorCode values are appended here so existing constants keep their
	// numeric values.
	CodeOutOfRange
)

// String returns a human-readable name for the ErrorCode, matching the
// constant's name without the "Code" prefix (e.g. CodeNotFound -> "NotFound").
func (c ErrorCode) String() string {
	switch c {
	case CodeNotFound:
		return "NotFound"
	case CodeAlreadyExists:
		return "AlreadyExists"
	case CodeInvalidArgument:
		return "InvalidArgument"
	case CodeFailedPrecondition:
		return "FailedPrecondition"
	case CodePermissionDenied:
		return "PermissionDenied"
	case CodeUnauthenticated:
		return "Unauthenticated"
	case CodeUnavailable:
		return "Unavailable"
	case CodeResourceExhausted:
		return "ResourceExhausted"
	case CodeAborted:
		return "Aborted"
	case CodeDeadlineExceeded:
		return "DeadlineExceeded"
	case CodeCanceled:
		return "Canceled"
	case CodeInternal:
		return "Internal"
	case CodeOutOfRange:
		return "OutOfRange"
	default:
		return "Unknown"
	}
}

// grpcCodeToErrorCode maps a gRPC status code to its native ErrorCode
// equivalent. Codes with no entry (including any not listed here) map to
// CodeUnknown via the zero value returned by the map lookup.
var grpcCodeToErrorCode = map[codes.Code]ErrorCode{
	codes.NotFound:           CodeNotFound,
	codes.AlreadyExists:      CodeAlreadyExists,
	codes.InvalidArgument:    CodeInvalidArgument,
	codes.FailedPrecondition: CodeFailedPrecondition,
	codes.PermissionDenied:   CodePermissionDenied,
	codes.Unauthenticated:    CodeUnauthenticated,
	codes.Unavailable:        CodeUnavailable,
	codes.ResourceExhausted:  CodeResourceExhausted,
	codes.Aborted:            CodeAborted,
	codes.DeadlineExceeded:   CodeDeadlineExceeded,
	codes.Canceled:           CodeCanceled,
	codes.Internal:           CodeInternal,
	codes.OutOfRange:         CodeOutOfRange,
}

// Error is a native SpiceDB error carrying a native error code and message.
// It satisfies errors.Is against the sentinel matching its Code, and Unwrap
// exposes the underlying gRPC status error for advanced inspection.
type Error struct {
	Code    ErrorCode
	Message string

	// Reason is SpiceDB's structured explanation for the failure: the name of
	// an authzed.api.v1.ErrorReason enum value, e.g.
	// "ERROR_REASON_MAXIMUM_DEPTH_EXCEEDED". It comes from the
	// google.rpc.ErrorInfo detail on the server's status and is empty when the
	// server attached none.
	//
	// The value is surfaced exactly as the server sent it. A reason a newer
	// server knows and this client does not is passed through unchanged rather
	// than coerced or rejected -- it is server-supplied, and root DESIGN.md's
	// "A conversion that cannot preserve meaning must fail" requires
	// server-supplied unknowns to degrade safely rather than raise.
	Reason string

	// ReasonDomain is the ErrorInfo's domain, identifying who produced the
	// reason. SpiceDB uses "authzed.com".
	ReasonDomain string

	// ReasonMetadata is the ErrorInfo's metadata: the specifics behind the
	// reason, such as which precondition failed or what depth limit was hit.
	// Nil when the server attached no ErrorInfo.
	ReasonMetadata map[string]string

	err error // underlying gRPC status error
}

func (e *Error) Error() string { return e.Message }
func (e *Error) Unwrap() error { return e.err }
func (e *Error) Is(target error) bool {
	switch target {
	case ErrNotFound:
		return e.Code == CodeNotFound
	case ErrAlreadyExists:
		return e.Code == CodeAlreadyExists
	case ErrInvalidArgument:
		return e.Code == CodeInvalidArgument
	case ErrFailedPrecondition:
		return e.Code == CodeFailedPrecondition
	case ErrPermissionDenied:
		return e.Code == CodePermissionDenied
	case ErrUnauthenticated:
		return e.Code == CodeUnauthenticated
	case ErrUnavailable:
		return e.Code == CodeUnavailable
	case ErrCanceled:
		return e.Code == CodeCanceled
	case ErrDeadlineExceeded:
		return e.Code == CodeDeadlineExceeded
	case ErrResourceExhausted:
		return e.Code == CodeResourceExhausted
	case ErrOutOfRange:
		return e.Code == CodeOutOfRange
	}
	return false
}

// errorInfoFrom returns the google.rpc.ErrorInfo detail attached to st, or nil
// if there is none. Status.Details does the unmarshalling; details it cannot
// resolve come back as errors and are simply skipped, so a detail type this
// client does not know about never prevents finding the one it does.
func errorInfoFrom(st *status.Status) *errdetails.ErrorInfo {
	if st == nil {
		return nil
	}
	for _, detail := range st.Details() {
		if info, ok := detail.(*errdetails.ErrorInfo); ok {
			return info
		}
	}
	return nil
}

// mapGRPCError wraps a gRPC error from operation op into a native *Error.
// Returns nil if err is nil.
//
// The server's status object is preserved on the returned error (reachable via
// errors.Unwrap plus google.golang.org/grpc/status), and its ErrorInfo detail
// is parsed onto Reason/ReasonDomain/ReasonMetadata, so nothing the server said
// is lost. See root DESIGN.md, "Error mapping must not lose the server's
// detail".
func mapGRPCError(op string, err error) error {
	if err == nil {
		return nil
	}
	st, _ := status.FromError(err)
	mapped := &Error{
		Code:    grpcCodeToErrorCode[st.Code()],
		Message: fmt.Sprintf("spicedb: %s: %s", op, st.Message()),
		err:     err,
	}
	if info := errorInfoFrom(st); info != nil {
		mapped.Reason = info.GetReason()
		mapped.ReasonDomain = info.GetDomain()
		// Copied, not aliased. Today this is defence in depth rather than a
		// fix: Status.Details unmarshals a fresh ErrorInfo on every call, so
		// the map GetMetadata returns is already private to this error. Copying
		// keeps that true if grpc-go ever caches the unmarshalled details, and
		// matches what the other six clients do -- a caller who mutates
		// ReasonMetadata must never be able to reach the status that Unwrap
		// still exposes.
		if metadata := info.GetMetadata(); len(metadata) > 0 {
			mapped.ReasonMetadata = make(map[string]string, len(metadata))
			for k, v := range metadata {
				mapped.ReasonMetadata[k] = v
			}
		}
	}
	return mapped
}
