package com.authzed.spicedb;

import com.authzed.spicedb.errors.*;

import io.grpc.Status;
import io.grpc.StatusRuntimeException;

import org.junit.jupiter.api.Test;

import static org.junit.jupiter.api.Assertions.*;

class ErrorMapperTest {

    @Test
    void permissionDeniedMapsCorrectly() {
        StatusRuntimeException e = new StatusRuntimeException(
            Status.PERMISSION_DENIED.withDescription("forbidden"));
        SpiceDBException mapped = ErrorMapper.toSpiceDBException(e);
        assertInstanceOf(PermissionDeniedException.class, mapped);
        assertEquals("forbidden", mapped.getMessage());
    }

    @Test
    void notFoundMapsCorrectly() {
        StatusRuntimeException e = new StatusRuntimeException(
            Status.NOT_FOUND.withDescription("not found"));
        SpiceDBException mapped = ErrorMapper.toSpiceDBException(e);
        assertInstanceOf(NotFoundException.class, mapped);
    }

    @Test
    void alreadyExistsMapsCorrectly() {
        StatusRuntimeException e = new StatusRuntimeException(
            Status.ALREADY_EXISTS.withDescription("already exists"));
        SpiceDBException mapped = ErrorMapper.toSpiceDBException(e);
        assertInstanceOf(AlreadyExistsException.class, mapped);
    }

    @Test
    void invalidArgumentMapsCorrectly() {
        StatusRuntimeException e = new StatusRuntimeException(
            Status.INVALID_ARGUMENT.withDescription("bad arg"));
        SpiceDBException mapped = ErrorMapper.toSpiceDBException(e);
        assertInstanceOf(InvalidArgumentException.class, mapped);
    }

    @Test
    void unknownCodeMapsToBaseException() {
        StatusRuntimeException e = new StatusRuntimeException(
            Status.INTERNAL.withDescription("internal error"));
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
    void isTransientForResourceExhausted() {
        StatusRuntimeException e = new StatusRuntimeException(Status.RESOURCE_EXHAUSTED);
        assertTrue(ErrorMapper.isTransient(e));
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
        StatusRuntimeException e = new StatusRuntimeException(
            Status.PERMISSION_DENIED.withDescription("forbidden"));
        SpiceDBException mapped = ErrorMapper.toSpiceDBException(e);
        assertSame(e, mapped.getCause());
    }
}
