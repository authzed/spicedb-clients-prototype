package com.authzed.spicedb;

import static org.junit.jupiter.api.Assertions.*;

import com.authzed.spicedb.errors.*;
import io.grpc.Status;
import io.grpc.StatusRuntimeException;
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
}
