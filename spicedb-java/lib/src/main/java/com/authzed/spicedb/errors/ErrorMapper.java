package com.authzed.spicedb.errors;

import io.grpc.Status;
import io.grpc.StatusRuntimeException;
import java.util.Set;

/**
 * Maps gRPC {@link StatusRuntimeException} to typed SpiceDB exceptions.
 *
 * <p>Transient error codes (UNAVAILABLE, ABORTED) are identified by {@link
 * #isTransient(StatusRuntimeException)} for retry logic. RESOURCE_EXHAUSTED is deliberately
 * excluded: in SpiceDB it signals either memory load-shed (retrying adds load to an
 * already-overloaded server) or a deterministic MaxDepthExceeded (retrying can never succeed -- it
 * just re-runs the most expensive class of check several times before surfacing the same error).
 * See root DESIGN.md, "Automatic retry is for idempotent operations only".
 */
public final class ErrorMapper {

  private static final Set<Status.Code> TRANSIENT_CODES =
      Set.of(Status.Code.UNAVAILABLE, Status.Code.ABORTED);

  private ErrorMapper() {}

  /**
   * Converts a gRPC {@link StatusRuntimeException} to a typed SpiceDB exception.
   *
   * <p>The originating exception is passed through as the cause, which is also where the returned
   * exception's {@link SpiceDBException#getReason()} and {@link
   * SpiceDBException#getReasonMetadata()} come from -- SpiceDB's {@code google.rpc.ErrorInfo}
   * detail. See root DESIGN.md, "RULE: Error mapping must not lose the server's detail".
   *
   * @param e the gRPC exception
   * @return a typed {@link SpiceDBException} subclass
   */
  public static SpiceDBException toSpiceDBException(StatusRuntimeException e) {
    String message = e.getStatus().getDescription();
    if (message == null) {
      message = e.getMessage();
    }

    return switch (e.getStatus().getCode()) {
      case PERMISSION_DENIED -> new PermissionDeniedException(message, e);
      case NOT_FOUND -> new NotFoundException(message, e);
      case ALREADY_EXISTS -> new AlreadyExistsException(message, e);
      case INVALID_ARGUMENT -> new InvalidArgumentException(message, e);
      case FAILED_PRECONDITION -> new FailedPreconditionException(message, e);
      case UNAVAILABLE -> new UnavailableException(message, e);
      case CANCELLED -> new CancelledException(message, e);
      case DEADLINE_EXCEEDED -> new DeadlineExceededException(message, e);
      case RESOURCE_EXHAUSTED -> new ResourceExhaustedException(message, e);
      case UNAUTHENTICATED -> new UnauthenticatedException(message, e);
      case OUT_OF_RANGE -> new OutOfRangeException(message, e);
      default -> new SpiceDBException(message, e);
    };
  }

  /** Returns {@code true} if the error is transient and worth retrying. */
  public static boolean isTransient(StatusRuntimeException e) {
    return TRANSIENT_CODES.contains(e.getStatus().getCode());
  }
}
