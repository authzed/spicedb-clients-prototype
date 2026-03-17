package com.authzed.spicedb.errors;

import io.grpc.Status;
import io.grpc.StatusRuntimeException;

import java.util.Set;

/**
 * Maps gRPC {@link StatusRuntimeException} to typed SpiceDB exceptions.
 *
 * <p>Transient error codes (UNAVAILABLE, RESOURCE_EXHAUSTED, ABORTED) are
 * identified by {@link #isTransient(StatusRuntimeException)} for retry logic.
 */
public final class ErrorMapper {

    private static final Set<Status.Code> TRANSIENT_CODES = Set.of(
        Status.Code.UNAVAILABLE,
        Status.Code.RESOURCE_EXHAUSTED,
        Status.Code.ABORTED
    );

    private ErrorMapper() { }

    /**
     * Converts a gRPC {@link StatusRuntimeException} to a typed SpiceDB exception.
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
            default -> new SpiceDBException(message, e);
        };
    }

    /**
     * Returns {@code true} if the error is transient and worth retrying.
     */
    public static boolean isTransient(StatusRuntimeException e) {
        return TRANSIENT_CODES.contains(e.getStatus().getCode());
    }
}
