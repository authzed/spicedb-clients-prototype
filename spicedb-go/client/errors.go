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
)

// Error is a native SpiceDB error carrying the gRPC status code and message.
// It satisfies errors.Is against the sentinel matching its Code, and Unwrap
// exposes the underlying gRPC status error for advanced inspection.
type Error struct {
	Code    codes.Code
	Message string
	err     error // underlying gRPC status error
}

func (e *Error) Error() string { return e.Message }
func (e *Error) Unwrap() error { return e.err }
func (e *Error) Is(target error) bool {
	switch target {
	case ErrNotFound:
		return e.Code == codes.NotFound
	case ErrAlreadyExists:
		return e.Code == codes.AlreadyExists
	case ErrInvalidArgument:
		return e.Code == codes.InvalidArgument
	case ErrFailedPrecondition:
		return e.Code == codes.FailedPrecondition
	case ErrPermissionDenied:
		return e.Code == codes.PermissionDenied
	case ErrUnauthenticated:
		return e.Code == codes.Unauthenticated
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
		Code:    st.Code(),
		Message: fmt.Sprintf("spicedb: %s: %s", op, st.Message()),
		err:     err,
	}
}
