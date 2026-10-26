package client

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestMappedCancellationSupportsStandardContextErrors(t *testing.T) {
	for _, tc := range []struct {
		code         codes.Code
		contextError error
		clientError  error
	}{
		{codes.Canceled, context.Canceled, ErrCanceled},
		{codes.DeadlineExceeded, context.DeadlineExceeded, ErrDeadlineExceeded},
	} {
		t.Run(tc.code.String(), func(t *testing.T) {
			source := status.Error(tc.code, "operation stopped")
			err := mapGRPCError("check", source)
			if !errors.Is(err, tc.contextError) {
				t.Errorf("mapped error must match %v; got %v", tc.contextError, err)
			}
			if !errors.Is(err, tc.clientError) {
				t.Errorf("mapped error lost SpiceDB identity %v", tc.clientError)
			}
			if !errors.Is(err, source) {
				t.Error("mapped error lost its original status")
			}
		})
	}
}
