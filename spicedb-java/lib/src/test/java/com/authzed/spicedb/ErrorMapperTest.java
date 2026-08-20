package com.authzed.spicedb;

import static org.junit.jupiter.api.Assertions.*;

import build.buf.gen.authzed.api.v1.ErrorReason;
import com.authzed.spicedb.errors.*;
import com.google.protobuf.Any;
import com.google.rpc.ErrorInfo;
import io.grpc.Status;
import io.grpc.StatusRuntimeException;
import io.grpc.protobuf.StatusProto;
import java.util.Map;
import org.junit.jupiter.api.Test;

class ErrorMapperTest {

  @Test
  void permissionDeniedMapsCorrectly() {
    StatusRuntimeException e =
        new StatusRuntimeException(Status.PERMISSION_DENIED.withDescription("forbidden"));
    SpiceDBException mapped = ErrorMapper.toSpiceDBException(e);
    assertInstanceOf(PermissionDeniedException.class, mapped);
    assertEquals("forbidden", mapped.getMessage());
  }

  @Test
  void notFoundMapsCorrectly() {
    StatusRuntimeException e =
        new StatusRuntimeException(Status.NOT_FOUND.withDescription("not found"));
    SpiceDBException mapped = ErrorMapper.toSpiceDBException(e);
    assertInstanceOf(NotFoundException.class, mapped);
  }

  @Test
  void alreadyExistsMapsCorrectly() {
    StatusRuntimeException e =
        new StatusRuntimeException(Status.ALREADY_EXISTS.withDescription("already exists"));
    SpiceDBException mapped = ErrorMapper.toSpiceDBException(e);
    assertInstanceOf(AlreadyExistsException.class, mapped);
  }

  @Test
  void invalidArgumentMapsCorrectly() {
    StatusRuntimeException e =
        new StatusRuntimeException(Status.INVALID_ARGUMENT.withDescription("bad arg"));
    SpiceDBException mapped = ErrorMapper.toSpiceDBException(e);
    assertInstanceOf(InvalidArgumentException.class, mapped);
  }

  @Test
  void failedPreconditionMapsCorrectly() {
    StatusRuntimeException e =
        new StatusRuntimeException(Status.FAILED_PRECONDITION.withDescription("boom"));
    SpiceDBException mapped = ErrorMapper.toSpiceDBException(e);
    assertInstanceOf(FailedPreconditionException.class, mapped);
  }

  @Test
  void unavailableMapsCorrectly() {
    StatusRuntimeException e =
        new StatusRuntimeException(Status.UNAVAILABLE.withDescription("down"));
    SpiceDBException mapped = ErrorMapper.toSpiceDBException(e);
    assertInstanceOf(UnavailableException.class, mapped);
  }

  @Test
  void cancelledMapsCorrectly() {
    StatusRuntimeException e =
        new StatusRuntimeException(Status.CANCELLED.withDescription("cancelled"));
    SpiceDBException mapped = ErrorMapper.toSpiceDBException(e);
    assertInstanceOf(CancelledException.class, mapped);
  }

  @Test
  void deadlineExceededMapsCorrectly() {
    StatusRuntimeException e =
        new StatusRuntimeException(Status.DEADLINE_EXCEEDED.withDescription("timeout"));
    SpiceDBException mapped = ErrorMapper.toSpiceDBException(e);
    assertInstanceOf(DeadlineExceededException.class, mapped);
  }

  @Test
  void resourceExhaustedMapsCorrectly() {
    StatusRuntimeException e =
        new StatusRuntimeException(Status.RESOURCE_EXHAUSTED.withDescription("quota"));
    SpiceDBException mapped = ErrorMapper.toSpiceDBException(e);
    assertInstanceOf(ResourceExhaustedException.class, mapped);
  }

  @Test
  void unknownCodeMapsToBaseException() {
    StatusRuntimeException e =
        new StatusRuntimeException(Status.INTERNAL.withDescription("internal error"));
    SpiceDBException mapped = ErrorMapper.toSpiceDBException(e);
    assertInstanceOf(SpiceDBException.class, mapped);
    assertFalse(mapped instanceof PermissionDeniedException);
    assertFalse(mapped instanceof NotFoundException);
  }

  @Test
  void nullDescriptionFallsBackToMessage() {
    StatusRuntimeException e = new StatusRuntimeException(Status.NOT_FOUND);
    SpiceDBException mapped = ErrorMapper.toSpiceDBException(e);
    assertInstanceOf(NotFoundException.class, mapped);
    assertNotNull(mapped.getMessage());
  }

  @Test
  void isTransientForUnavailable() {
    StatusRuntimeException e = new StatusRuntimeException(Status.UNAVAILABLE);
    assertTrue(ErrorMapper.isTransient(e));
  }

  @Test
  void isNotTransientForResourceExhausted() {
    // Inverted from "isTransientForResourceExhausted" / assertTrue -- RESOURCE_EXHAUSTED must NOT
    // be retried. In SpiceDB it signals memory load-shed or a deterministic MaxDepthExceeded,
    // never a transient hiccup. See DESIGN.md, "Automatic retry is for idempotent operations
    // only".
    StatusRuntimeException e = new StatusRuntimeException(Status.RESOURCE_EXHAUSTED);
    assertFalse(ErrorMapper.isTransient(e));
  }

  @Test
  void isTransientForAborted() {
    StatusRuntimeException e = new StatusRuntimeException(Status.ABORTED);
    assertTrue(ErrorMapper.isTransient(e));
  }

  @Test
  void isNotTransientForPermissionDenied() {
    StatusRuntimeException e = new StatusRuntimeException(Status.PERMISSION_DENIED);
    assertFalse(ErrorMapper.isTransient(e));
  }

  @Test
  void isNotTransientForNotFound() {
    StatusRuntimeException e = new StatusRuntimeException(Status.NOT_FOUND);
    assertFalse(ErrorMapper.isTransient(e));
  }

  @Test
  void causeIsPreserved() {
    StatusRuntimeException e =
        new StatusRuntimeException(Status.PERMISSION_DENIED.withDescription("forbidden"));
    SpiceDBException mapped = ErrorMapper.toSpiceDBException(e);
    assertSame(e, mapped.getCause());
  }

  /**
   * Builds a StatusRuntimeException carrying a {@code google.rpc.ErrorInfo} detail, the shape
   * SpiceDB uses to explain a failure.
   */
  private static StatusRuntimeException withErrorInfo(
      Status.Code code, String message, ErrorInfo info) {
    return StatusProto.toStatusRuntimeException(
        com.google.rpc.Status.newBuilder()
            .setCode(code.value())
            .setMessage(message)
            .addDetails(Any.pack(info))
            .build());
  }

  // OUT_OF_RANGE is SpiceDB's code for an expired or garbage-collected ZedToken. Recovery is
  // mechanical -- drop the token, re-read at full consistency -- so it must be distinguishable by
  // type rather than by message. See root DESIGN.md, "RULE: Error mapping must not lose the
  // server's detail".
  @Test
  void outOfRangeMapsToItsOwnType() {
    StatusRuntimeException e =
        new StatusRuntimeException(
            Status.OUT_OF_RANGE.withDescription("revision no longer available"));
    SpiceDBException mapped = ErrorMapper.toSpiceDBException(e);
    assertInstanceOf(OutOfRangeException.class, mapped);
    assertFalse(mapped instanceof InvalidArgumentException);
    assertFalse(mapped instanceof FailedPreconditionException);
    assertEquals("revision no longer available", mapped.getMessage());
  }

  // A wrong, expired, or rotated token must be distinguishable from an internal server fault, so a
  // caller can refresh credentials on one and page someone on the other.
  @Test
  void unauthenticatedMapsToItsOwnType() {
    StatusRuntimeException e =
        new StatusRuntimeException(Status.UNAUTHENTICATED.withDescription("bad token"));
    SpiceDBException mapped = ErrorMapper.toSpiceDBException(e);
    assertInstanceOf(UnauthenticatedException.class, mapped);
    assertFalse(mapped instanceof PermissionDeniedException);
    assertNotSame(SpiceDBException.class, mapped.getClass());
  }

  @Test
  void neitherNewlyMappedCodeIsTransient() {
    assertFalse(ErrorMapper.isTransient(new StatusRuntimeException(Status.OUT_OF_RANGE)));
    assertFalse(ErrorMapper.isTransient(new StatusRuntimeException(Status.UNAUTHENTICATED)));
  }

  @Test
  void errorReasonAndMetadataSurviveMapping() {
    SpiceDBException mapped =
        ErrorMapper.toSpiceDBException(
            withErrorInfo(
                Status.Code.RESOURCE_EXHAUSTED,
                "max depth exceeded",
                ErrorInfo.newBuilder()
                    .setReason("ERROR_REASON_MAXIMUM_DEPTH_EXCEEDED")
                    .setDomain("authzed.com")
                    .putMetadata("maximum_depth_allowed", "50")
                    .build()));

    assertInstanceOf(ResourceExhaustedException.class, mapped);
    // The exposed reason is exactly the authzed.api.v1.ErrorReason enum name, so a caller can
    // compare against the generated enum without this client carrying a hand-maintained copy of
    // it.
    assertEquals("ERROR_REASON_MAXIMUM_DEPTH_EXCEEDED", mapped.getReason());
    assertEquals(ErrorReason.ERROR_REASON_MAXIMUM_DEPTH_EXCEEDED.name(), mapped.getReason());
    assertEquals("authzed.com", mapped.getReasonDomain());
    assertEquals(Map.of("maximum_depth_allowed", "50"), mapped.getReasonMetadata());
  }

  @Test
  void reasonMetadataNamesTheFailingPrecondition() {
    SpiceDBException mapped =
        ErrorMapper.toSpiceDBException(
            withErrorInfo(
                Status.Code.FAILED_PRECONDITION,
                "precondition failed",
                ErrorInfo.newBuilder()
                    .setReason("ERROR_REASON_WRITE_OR_DELETE_PRECONDITION_FAILURE")
                    .setDomain("authzed.com")
                    .putMetadata("precondition_resource_id", "firstdoc")
                    .putMetadata("precondition_relation", "viewer")
                    .build()));

    assertInstanceOf(FailedPreconditionException.class, mapped);
    assertEquals("ERROR_REASON_WRITE_OR_DELETE_PRECONDITION_FAILURE", mapped.getReason());
    assertEquals("firstdoc", mapped.getReasonMetadata().get("precondition_resource_id"));
    assertEquals("viewer", mapped.getReasonMetadata().get("precondition_relation"));
  }

  // A reason a newer server knows and this client does not is server-supplied: root DESIGN.md's
  // "RULE: A conversion that cannot preserve meaning must fail" requires it to degrade safely, not
  // to throw.
  @Test
  void unrecognizedReasonPassesThroughWithoutThrowing() {
    SpiceDBException mapped =
        ErrorMapper.toSpiceDBException(
            withErrorInfo(
                Status.Code.INVALID_ARGUMENT,
                "from the future",
                ErrorInfo.newBuilder()
                    .setReason("ERROR_REASON_INVENTED_BY_A_NEWER_SERVER")
                    .setDomain("authzed.com")
                    .putMetadata("k", "v")
                    .build()));

    assertInstanceOf(InvalidArgumentException.class, mapped);
    assertEquals("ERROR_REASON_INVENTED_BY_A_NEWER_SERVER", mapped.getReason());
    assertEquals(Map.of("k", "v"), mapped.getReasonMetadata());
  }

  @Test
  void absentErrorInfoLeavesReasonEmpty() {
    SpiceDBException mapped =
        ErrorMapper.toSpiceDBException(
            new StatusRuntimeException(Status.NOT_FOUND.withDescription("nope")));
    assertEquals("", mapped.getReason());
    assertEquals("", mapped.getReasonDomain());
    assertTrue(mapped.getReasonMetadata().isEmpty());
  }

  @Test
  void anUnfamiliarDetailDoesNotHideTheErrorInfo() {
    StatusRuntimeException e =
        StatusProto.toStatusRuntimeException(
            com.google.rpc.Status.newBuilder()
                .setCode(Status.Code.FAILED_PRECONDITION.value())
                .setMessage("precondition failed")
                .addDetails(Any.pack(com.google.rpc.RetryInfo.getDefaultInstance()))
                .addDetails(
                    Any.pack(
                        ErrorInfo.newBuilder()
                            .setReason("ERROR_REASON_EMPTY_PRECONDITION")
                            .setDomain("authzed.com")
                            .build()))
                .build());

    SpiceDBException mapped = ErrorMapper.toSpiceDBException(e);
    assertEquals("ERROR_REASON_EMPTY_PRECONDITION", mapped.getReason());
  }

  @Test
  void reasonIsEmptyForALocallyConstructedException() {
    assertEquals("", new SpiceDBException("local problem").getReason());
    assertTrue(new SpiceDBException("local problem").getReasonMetadata().isEmpty());
  }
}
