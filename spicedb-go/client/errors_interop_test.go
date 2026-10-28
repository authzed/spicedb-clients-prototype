package client

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var interopCodes = map[ErrorCode]codes.Code{
	CodeUnknown:            codes.Unknown,
	CodeNotFound:           codes.NotFound,
	CodeAlreadyExists:      codes.AlreadyExists,
	CodeInvalidArgument:    codes.InvalidArgument,
	CodeFailedPrecondition: codes.FailedPrecondition,
	CodePermissionDenied:   codes.PermissionDenied,
	CodeUnauthenticated:    codes.Unauthenticated,
	CodeUnavailable:        codes.Unavailable,
	CodeResourceExhausted:  codes.ResourceExhausted,
	CodeAborted:            codes.Aborted,
	CodeDeadlineExceeded:   codes.DeadlineExceeded,
	CodeCanceled:           codes.Canceled,
	CodeInternal:           codes.Internal,
	CodeOutOfRange:         codes.OutOfRange,
	CodeUnimplemented:      codes.Unimplemented,
	CodeDataLoss:           codes.DataLoss,
}

func TestStatusCodeAgreesForEveryErrorSource(t *testing.T) {
	for native, grpcCode := range interopCodes {
		t.Run(native.String(), func(t *testing.T) {
			fromServer := mapGRPCError("op", status.Error(grpcCode, "boom"))
			built := NewError(native, "boom")
			wrapped := fmt.Errorf("outer: %w", built)
			for name, err := range map[string]error{"server": fromServer, "NewError": built, "wrapped": wrapped} {
				require.Equal(t, grpcCode, status.Code(err), name)
				require.Equal(t, native, CodeOf(err), name)
				st, ok := status.FromError(err)
				require.True(t, ok, name)
				require.Equal(t, grpcCode, st.Code(), name)
			}
		})
	}
}

func TestLocalCancellationReportsStatusCode(t *testing.T) {
	canceled := mapGRPCError("check", context.Canceled)
	require.Equal(t, codes.Canceled, status.Code(canceled))
	require.ErrorIs(t, canceled, context.Canceled)
	require.ErrorIs(t, canceled, ErrCanceled)
	require.Empty(t, canceled.(*Error).ServerMessage, "local cancellation is not a server message")

	deadline := mapGRPCError("check", fmt.Errorf("dial: %w", context.DeadlineExceeded))
	require.Equal(t, codes.DeadlineExceeded, status.Code(deadline))

	local := invalid("timeout must be positive")
	require.Equal(t, codes.InvalidArgument, status.Code(local))
}

func TestServerStatusPreservedThroughGRPCStatus(t *testing.T) {
	st, err := status.New(codes.FailedPrecondition, "precondition failed").WithDetails(&errdetails.ErrorInfo{
		Reason: "ERROR_REASON_WRITE_OR_DELETE_PRECONDITION_FAILURE", Domain: "authzed.com",
	})
	require.NoError(t, err)

	mapped := mapGRPCError("write", st.Err())
	var native *Error
	require.ErrorAs(t, mapped, &native)
	require.Equal(t, "precondition failed", native.ServerMessage)
	require.Equal(t, "spicedb: write: precondition failed", native.Message)

	got := status.Convert(mapped)
	require.Equal(t, "precondition failed", got.Message())
	require.Len(t, got.Details(), 1, "server details survive GRPCStatus")
}

func TestNewErrorSatisfiesSentinels(t *testing.T) {
	err := NewError(CodeFailedPrecondition, "nope")
	require.ErrorIs(t, err, ErrFailedPrecondition)
	require.NotErrorIs(t, err, ErrNotFound)
	require.Equal(t, "nope", err.Error())
	require.Equal(t, "nope", err.ServerMessage)
}

func TestCodeOfClassifiesRawStatusErrors(t *testing.T) {
	require.Equal(t, CodeFailedPrecondition, CodeOf(status.Errorf(codes.FailedPrecondition, "x")))
	require.Equal(t, CodeCanceled, CodeOf(context.Canceled))
	require.Equal(t, CodeUnknown, CodeOf(errors.New("plain")))
	require.Equal(t, CodeUnknown, CodeOf(nil))
}
