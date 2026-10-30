package com.authzed.spicedb;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertInstanceOf;
import static org.junit.jupiter.api.Assertions.assertSame;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import com.authzed.api.v1.CheckBulkPermissionsPair;
import com.authzed.api.v1.CheckBulkPermissionsRequest;
import com.authzed.api.v1.CheckBulkPermissionsResponse;
import com.authzed.api.v1.CheckPermissionRequest;
import com.authzed.api.v1.CheckPermissionResponse;
import com.authzed.api.v1.PermissionsServiceGrpc;
import com.authzed.spicedb.errors.AlreadyExistsException;
import com.authzed.spicedb.errors.CancelledException;
import com.authzed.spicedb.errors.ErrorMapper;
import com.authzed.spicedb.errors.FailedPreconditionException;
import com.authzed.spicedb.errors.InvalidArgumentException;
import com.authzed.spicedb.errors.NotFoundException;
import com.authzed.spicedb.errors.PermissionDeniedException;
import com.authzed.spicedb.errors.SpiceDBException;
import io.grpc.Status;
import io.grpc.StatusRuntimeException;
import io.grpc.stub.StreamObserver;
import java.util.List;
import java.util.concurrent.CancellationException;
import org.junit.jupiter.api.Test;

/** Native errors interoperate with gRPC status inspection in both directions. */
class ErrorInteropTest {

  private static PermissionCheck aCheck() {
    return PermissionCheck.builder()
        .resource(new ObjectRef("document", "doc1"))
        .permission("view")
        .subject(SubjectRef.direct(new ObjectRef("user", "alice")))
        .consistency(Consistency.full())
        .build();
  }

  @Test
  void serverErrorReportsItsStatusAndVerbatimServerMessage() throws Exception {
    var service =
        new PermissionsServiceGrpc.PermissionsServiceImplBase() {
          @Override
          public void checkPermission(
              CheckPermissionRequest request, StreamObserver<CheckPermissionResponse> response) {
            response.onError(
                Status.FAILED_PRECONDITION.withDescription("exact server words").asException());
          }
        };
    try (var servers = TestServers.start(service)) {
      var e =
          assertThrows(FailedPreconditionException.class, () -> servers.client().check(aCheck()));
      assertEquals(Status.Code.FAILED_PRECONDITION, e.code());
      assertEquals(Status.Code.FAILED_PRECONDITION, Status.fromThrowable(e).getCode());
      assertEquals(Status.Code.FAILED_PRECONDITION, ErrorMapper.codeOf(e));
      assertEquals("exact server words", e.serverMessage());
      assertEquals("exact server words", e.status().getDescription());
    }
  }

  @Test
  void localValidationErrorsReportInvalidArgument() {
    var e = assertThrows(InvalidArgumentException.class, () -> Filter.of(""));
    assertEquals(Status.Code.INVALID_ARGUMENT, e.code());
    assertEquals(Status.Code.INVALID_ARGUMENT, Status.fromThrowable(e).getCode());
    assertEquals(e.getMessage(), e.serverMessage(), "a local error's verbatim message is its own");
    assertInstanceOf(com.authzed.spicedb.errors.LocalValidationException.class, e);
  }

  @Test
  void serverInvalidArgumentIsNotLocalValidation() {
    var server = ErrorMapper.newError(Status.Code.INVALID_ARGUMENT, "relation `x` not found");
    assertInstanceOf(InvalidArgumentException.class, server);
    assertFalse(server instanceof com.authzed.spicedb.errors.LocalValidationException);
    assertEquals("relation `x` not found", server.serverMessage());
    var built = new SpiceDBException("caller-built");
    assertEquals("caller-built", built.serverMessage(), "never null");
  }

  @Test
  void callerConstructedErrorsReportTheirCode() {
    var cases =
        List.of(
            new AlreadyExistsException("dup"),
            new NotFoundException("gone"),
            new PermissionDeniedException("nope"),
            new CancelledException("stop"));
    var codes =
        List.of(
            Status.Code.ALREADY_EXISTS,
            Status.Code.NOT_FOUND,
            Status.Code.PERMISSION_DENIED,
            Status.Code.CANCELLED);
    for (int i = 0; i < cases.size(); i++) {
      SpiceDBException e = cases.get(i);
      assertEquals(codes.get(i), e.code());
      assertEquals(codes.get(i), Status.fromThrowable(e).getCode());
      assertEquals(codes.get(i), e.toStatusRuntimeException().getStatus().getCode());
    }
    // A base exception with a non-status cause still reports a status (UNKNOWN), and keeps the
    // original cause reachable underneath it.
    var io = new java.io.IOException("disk");
    var base = new SpiceDBException("wrapped", io);
    assertEquals(Status.Code.UNKNOWN, Status.fromThrowable(base).getCode());
    assertSame(io, base.getCause().getCause());
  }

  @Test
  void newErrorBuildsTheTypedErrorEveryPredicateAccepts() {
    var e = ErrorMapper.newError(Status.Code.FAILED_PRECONDITION, "guard");
    assertInstanceOf(FailedPreconditionException.class, e);
    assertEquals(Status.Code.FAILED_PRECONDITION, e.code());
    assertEquals(Status.Code.FAILED_PRECONDITION, Status.fromThrowable(e).getCode());
    assertEquals("guard", e.serverMessage());
    assertEquals("guard", e.getMessage());
  }

  @Test
  void codeOfClassifiesRawStatusAndCancellation() {
    assertEquals(
        Status.Code.ABORTED, ErrorMapper.codeOf(new StatusRuntimeException(Status.ABORTED)));
    assertEquals(
        Status.Code.UNAVAILABLE,
        ErrorMapper.codeOf(new RuntimeException("wrap", Status.UNAVAILABLE.asException())));
    // Raw status errors convert into the typed hierarchy for instanceof matching.
    assertInstanceOf(
        AlreadyExistsException.class,
        ErrorMapper.toSpiceDBException(Status.ALREADY_EXISTS.asException()));
    // The client's own cancellation is a CancellationException carrying a CANCELLED status.
    CancellationException c = SpiceDBClient.cancelled("operation interrupted");
    assertEquals(Status.Code.CANCELLED, ErrorMapper.codeOf(c));
    assertEquals(Status.Code.CANCELLED, Status.fromThrowable(c).getCode());
    assertEquals(Status.Code.CANCELLED, ErrorMapper.codeOf(new CancellationException("plain")));
    assertEquals(Status.Code.UNKNOWN, ErrorMapper.codeOf(new IllegalStateException()));
  }

  @Test
  void interruptedCallsRaiseCancellationThatClassifiesAsCancelled() throws Exception {
    var service = new PermissionsServiceGrpc.PermissionsServiceImplBase() {};
    try (var servers = TestServers.start(service)) {
      Thread.currentThread().interrupt();
      try {
        var e = assertThrows(CancellationException.class, () -> servers.client().check(aCheck()));
        assertEquals(Status.Code.CANCELLED, Status.fromThrowable(e).getCode());
      } finally {
        Thread.interrupted();
      }
    }
  }

  @Test
  void bulkItemErrorKeepsServerMessageAndAddsIndexToDisplayMessage() throws Exception {
    var service =
        new PermissionsServiceGrpc.PermissionsServiceImplBase() {
          @Override
          public void checkBulkPermissions(
              CheckBulkPermissionsRequest request,
              StreamObserver<CheckBulkPermissionsResponse> response) {
            response.onNext(
                CheckBulkPermissionsResponse.newBuilder()
                    .addPairs(
                        CheckBulkPermissionsPair.newBuilder()
                            .setRequest(request.getItems(0))
                            .setError(
                                com.google.rpc.Status.newBuilder()
                                    .setCode(Status.Code.NOT_FOUND.value())
                                    .setMessage("object definition `doc` not found")))
                    .build());
            response.onCompleted();
          }
        };
    try (var servers = TestServers.start(service)) {
      var e =
          assertThrows(
              NotFoundException.class, () -> servers.client().checkBulk(List.of(aCheck())));
      assertEquals("object definition `doc` not found", e.serverMessage());
      assertTrue(e.getMessage().startsWith("check item 0: "), e.getMessage());
      assertEquals(Status.Code.NOT_FOUND, Status.fromThrowable(e).getCode());
    }
  }
}
