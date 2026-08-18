package client

import (
	"errors"
	"fmt"

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
}

// Error is a native SpiceDB error carrying a native error code and message.
// It satisfies errors.Is against the sentinel matching its Code, and Unwrap
// exposes the underlying gRPC status error for advanced inspection.
type Error struct {
	Code    ErrorCode
	Message string
	err     error // underlying gRPC status error
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
	}
	return false
}

// mapGRPCError wraps a gRPC error from operation op into a native *Error.
// Returns nil if err is nil.
func mapGRPCError(op string, err error) error {
	if err == nil {
		return nil
	}
	st, _ := status.FromError(err)
	return &Error{
		Code:    grpcCodeToErrorCode[st.Code()],
		Message: fmt.Sprintf("spicedb: %s: %s", op, st.Message()),
		err:     err,
	}
}
