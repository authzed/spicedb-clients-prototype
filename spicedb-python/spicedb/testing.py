"""In-memory test doubles for the native clients.

:class:`InMemoryClient` stands in for :class:`spicedb.sync.SpiceDBClient` and
:class:`AsyncInMemoryClient` for :class:`spicedb.aio.SpiceDBClient`. Their
``write``, ``check``, ``check_bulk``, ``read_relationships``,
``delete_relationships``, ``delete_all_relationships_non_atomic``,
``import_relationships``, ``export_relationships``, ``read_schema``,
``write_schema`` and ``reflect_schema`` methods have the same signatures as
the real clients', so code written against a ``Protocol`` naming those
methods accepts either.

They apply SpiceDB's relationship semantics -- filter matching
(:meth:`spicedb.Filter.matches`), write preconditions, CREATE conflicts
(``AlreadyExistsError``), atomic transactions and imports, bounded and
all-or-nothing deletes, empty writes that only check preconditions -- and
raise errors shaped like the real client's for the same failure: server
failures are built with :meth:`spicedb.SpiceDBError.from_code` (so
``server_message`` holds the server's text and ``str(error)`` is that text),
and local validation runs the real clients' own request builders, raising the
same ``InvalidArgumentError`` (``server_message`` is None) before any state
changes.

They do not evaluate a schema: checks return results registered with
:meth:`InMemoryClient.set_check_result` (default ``NO_PERMISSION``),
``write_schema`` stores the text without compiling it, and
``reflect_schema`` returns the result registered with
:meth:`InMemoryClient.set_reflect_schema_result`.

Options are ordinary keyword arguments, so a test inspects them directly.
The doubles honor those that change results: read and export cursors, the
export filter, delete limits and modes, ``paging``, the per-call retry
opt-in rules of ``CallOptions(retry_policy=...)`` (rejected for mutations
that cannot replay, as the real clients do), and
``CallOptions(response_metadata=...)``, filled from
:meth:`InMemoryClient.set_response_metadata`. Consistency and timeouts are
validated and otherwise ignored. Both doubles are safe for use from
multiple threads.
"""

from __future__ import annotations

import inspect
import threading
from collections.abc import AsyncIterable, Callable, Iterable, Sequence
from dataclasses import replace
from typing import Any

import grpc

from spicedb import _requests
from spicedb._budget import Budget
from spicedb._client_base import mutation_retry_policy, response_target, write_is_idempotent
from spicedb._streams import AsyncStream, Stream
from spicedb.consistency import Consistency
from spicedb.errors import InvalidArgumentError, SpiceDBError
from spicedb.options import BulkItemErrors, CallOptions, DeletePartial, Paging, Tracing, _option
from spicedb.token import ZedToken
from spicedb.types import (
    CheckResult,
    DeleteProgress,
    DeleteResult,
    ExportedRelationship,
    Filter,
    JSONObject,
    ObjectRef,
    PermissionCheck,
    Permissionship,
    PreconditionOperation,
    ReadRelationship,
    ReflectSchemaResult,
    Relationship,
    SchemaFilter,
    SchemaResult,
    SubjectRef,
    Transaction,
    UpdateOperation,
    WriteResult,
)

__all__ = ["InMemoryClient", "AsyncInMemoryClient"]

# SpiceDB's default for a delete request without an explicit limit.
SERVER_DEFAULT_DELETE_LIMIT = 1000

_NO_SCHEMA = "No schema has been defined; please call WriteSchema to start"


def _key(relationship: Relationship):
    return (relationship.resource, relationship.relation, relationship.subject)


def _check_key(resource: ObjectRef, permission: str, subject: SubjectRef):
    return (resource, permission, subject)


def _precondition_error(text: str) -> SpiceDBError:
    return SpiceDBError.from_code(
        grpc.StatusCode.FAILED_PRECONDITION,
        f"unable to satisfy write precondition `{text}`",
        reason="ERROR_REASON_WRITE_OR_DELETE_PRECONDITION_FAILURE",
        reason_domain="authzed.com",
    )


def _call_options(call_options: CallOptions | None) -> None:
    if call_options is not None and not isinstance(call_options, CallOptions):
        raise InvalidArgumentError(f"call_options: expected CallOptions, got {call_options!r}")


def _positive(value, field):
    if isinstance(value, bool) or not isinstance(value, int) or value <= 0:
        raise InvalidArgumentError(f"{field}: expected positive integer")


class _Store:
    def __init__(self, relationships: Sequence[Relationship]) -> None:
        self.lock = threading.RLock()
        self.relationships: dict[Any, Relationship] = {}
        self.checks: dict[Any, CheckResult] = {}
        self.revision = 0
        self.schema: str | None = None
        self.reflection: ReflectSchemaResult | None = None
        self.headers: tuple[tuple[str, str | bytes], ...] = ()
        self.trailers: tuple[tuple[str, str | bytes], ...] = ()
        for relationship in relationships:
            if not isinstance(relationship, Relationship):
                raise InvalidArgumentError("relationships: expected Relationship values")
            self.relationships[_key(relationship)] = relationship

    def token(self) -> ZedToken:
        return ZedToken(str(self.revision))

    def respond(self, call_options: CallOptions | None) -> None:
        """Fill a ``ResponseMetadata`` target with the registered metadata."""
        target = response_target(call_options)
        if target is not None:
            with self.lock:
                target.headers, target.trailers = self.headers, self.trailers

    def matches_any(self, filter: Filter) -> bool:
        return any(filter.matches(r) for r in self.relationships.values())

    def check_preconditions(self, preconditions) -> None:
        for precondition in preconditions:
            matched = self.matches_any(precondition.filter)
            wanted = precondition.operation is PreconditionOperation.MUST_MATCH
            if matched != wanted:
                raise _precondition_error(str(precondition))

    def write(self, transaction: Transaction) -> WriteResult:
        with self.lock:
            self.check_preconditions(transaction.preconditions)
            if len(transaction) == 0:
                # SpiceDB accepts an empty write: it evaluates the
                # preconditions and returns the current revision.
                return WriteResult(self.token())
            staged = dict(self.relationships)
            for update in transaction.updates:
                key = _key(update.relationship)
                if update.operation is UpdateOperation.CREATE:
                    if key in staged:
                        raise SpiceDBError.from_code(
                            grpc.StatusCode.ALREADY_EXISTS,
                            f"could not CREATE relationship `{update.relationship}`, as it already existed",
                            reason="ERROR_REASON_ATTEMPT_TO_RECREATE_RELATIONSHIP",
                            reason_domain="authzed.com",
                        )
                    staged[key] = update.relationship
                elif update.operation is UpdateOperation.TOUCH:
                    staged[key] = update.relationship
                elif update.operation is UpdateOperation.DELETE:
                    staged.pop(key, None)
                else:
                    raise InvalidArgumentError("unknown update operation")
            self.relationships = staged
            self.revision += 1
            return WriteResult(self.token())

    def page(self, match: Callable[[Relationship], bool], cursor: str | None, operation: str):
        """Matching relationships after ``cursor``, each with its own cursor
        (the 1-based position it occupies in matching order)."""
        start = 0
        if cursor is not None:
            try:
                start = int(cursor)
            except ValueError:
                start = -1
            if start < 0:
                raise SpiceDBError.from_code(grpc.StatusCode.INVALID_ARGUMENT, f"{operation}: invalid cursor")
        with self.lock:
            matched = [r for r in self.relationships.values() if match(r)]
            return [(r, str(i + 1)) for i, r in enumerate(matched) if i >= start], self.token()

    def import_all(self, relationships: list[Relationship]) -> int:
        with self.lock:
            staged = dict(self.relationships)
            for relationship in relationships:
                key = _key(relationship)
                if key in staged:
                    raise SpiceDBError.from_code(
                        grpc.StatusCode.ALREADY_EXISTS,
                        f"found duplicate relationship `{relationship}` while importing",
                    )
                staged[key] = relationship
            self.relationships = staged
            self.revision += 1
            return len(relationships)

    def delete(self, filter, limit, allow_partial, must_match, must_not_match) -> DeleteResult:
        with self.lock:
            for f in must_match:
                if not self.matches_any(f):
                    raise _precondition_error(f"MUST_MATCH({f})")
            for f in must_not_match:
                if self.matches_any(f):
                    raise _precondition_error(f"MUST_NOT_MATCH({f})")
            bound = SERVER_DEFAULT_DELETE_LIMIT if limit is None else limit
            matched = [k for k, r in self.relationships.items() if filter.matches(r)]
            if not allow_partial and len(matched) > bound:
                raise SpiceDBError.from_code(
                    grpc.StatusCode.INVALID_ARGUMENT,
                    f"found more than {bound} relationships to be deleted and partial deletion was not requested",
                    reason="ERROR_REASON_TOO_MANY_RELATIONSHIPS_FOR_TRANSACTIONAL_DELETE",
                    reason_domain="authzed.com",
                )
            deleted = matched[:bound]
            for key in deleted:
                del self.relationships[key]
            self.revision += 1
            remaining = len(matched) > len(deleted)
            return DeleteResult(
                self.token(),
                len(deleted),
                DeleteProgress.PARTIAL if remaining else DeleteProgress.COMPLETE,
                None,
            )

    def check(self, check: PermissionCheck) -> CheckResult:
        with self.lock:
            result = self.checks.get(_check_key(check.resource, check.permission, check.subject))
            token = self.token()
        if result is None:
            return CheckResult(Permissionship.NO_PERMISSION, (), token)
        return replace(result, checked_at=token)


def _delete_args(limit, partial):
    allow_partial = _option(partial, DeletePartial) == "allowed"
    if allow_partial or limit is not None:
        _positive(limit, "limit")
    return allow_partial


class InMemoryClient:
    """In-memory stand-in for :class:`spicedb.sync.SpiceDBClient`."""

    def __init__(self, relationships: Sequence[Relationship] = ()) -> None:
        self._store = _Store(relationships)

    def relationships(self) -> list[Relationship]:
        """A snapshot of every stored relationship, in insertion order."""
        with self._store.lock:
            return list(self._store.relationships.values())

    def set_check_result(
        self,
        resource: ObjectRef,
        permission: str,
        subject: SubjectRef,
        result: CheckResult | Permissionship,
    ) -> None:
        """Register what ``check``/``check_bulk`` return for this triple."""
        if isinstance(result, Permissionship):
            result = CheckResult(result, (), ZedToken(""))
        if not isinstance(result, CheckResult):
            raise InvalidArgumentError("result: expected CheckResult or Permissionship")
        with self._store.lock:
            self._store.checks[_check_key(resource, permission, subject)] = result

    def set_reflect_schema_result(self, result: ReflectSchemaResult) -> None:
        """Register what ``reflect_schema`` returns (stamped with the current revision)."""
        if not isinstance(result, ReflectSchemaResult):
            raise InvalidArgumentError("result: expected ReflectSchemaResult")
        with self._store.lock:
            self._store.reflection = result

    def set_response_metadata(
        self,
        headers: Sequence[tuple[str, str | bytes]] = (),
        trailers: Sequence[tuple[str, str | bytes]] = (),
    ) -> None:
        """Set the headers and trailers every operation reports to a
        ``CallOptions(response_metadata=...)`` target."""
        with self._store.lock:
            self._store.headers = tuple(tuple(pair) for pair in headers)  # type: ignore[misc]
            self._store.trailers = tuple(tuple(pair) for pair in trailers)  # type: ignore[misc]

    def write(
        self,
        transaction: Transaction,
        *,
        metadata: JSONObject | None = None,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> WriteResult:
        """Apply the transaction atomically with SpiceDB semantics.

        An empty transaction checks its preconditions and returns the
        current revision, as SpiceDB does.
        """
        Budget(timeout)
        if not isinstance(transaction, Transaction):
            raise InvalidArgumentError("transaction: expected Transaction")
        mutation_retry_policy(call_options, write_is_idempotent(transaction), "write")
        _requests.write_request(transaction)
        _requests.context_struct(metadata)
        self._store.respond(call_options)
        return self._store.write(transaction)

    def check(
        self,
        check: PermissionCheck,
        *,
        consistency: Consistency,
        context: JSONObject | None = None,
        tracing: Tracing = Tracing.DISABLED,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> CheckResult:
        """Return the registered result, stamped with the current revision."""
        Budget(timeout)
        _call_options(call_options)
        _requests.check_request(check, consistency, context, tracing)
        self._store.respond(call_options)
        return self._store.check(check)

    def check_bulk(
        self,
        checks: Sequence[PermissionCheck],
        *,
        consistency: Consistency,
        context: JSONObject | None = None,
        tracing: Tracing = Tracing.DISABLED,
        item_errors: BulkItemErrors = BulkItemErrors.RAISE,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> list[CheckResult]:
        """Return the registered results in input order."""
        _option(item_errors, BulkItemErrors)
        Budget(timeout)
        _call_options(call_options)
        _requests.check_bulk_request(consistency, checks, context, tracing)
        self._store.respond(call_options)
        return [self._store.check(c) for c in checks]

    def read_relationships(
        self,
        filter: Filter,
        *,
        consistency: Consistency,
        page_size: int = 1000,
        cursor: str | None = None,
        paging: Paging = Paging.AUTOMATIC,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> Stream[ReadRelationship]:
        """Yield matching relationships (a snapshot) in insertion order,
        resuming after ``cursor``. Each item's cursor resumes after it."""
        _option(paging, Paging)
        Budget(timeout)
        _positive(page_size, "page_size")
        _call_options(call_options)
        if not isinstance(filter, Filter):
            raise InvalidArgumentError("filter: expected Filter")
        _requests.read_relationships_request(filter, consistency, page_size, None)
        if cursor is not None and (not isinstance(cursor, str) or not cursor):
            raise InvalidArgumentError("cursor: expected nonempty cursor string")
        store = self._store

        def factory(_owner):
            items, read_at = store.page(filter.matches, cursor, "read relationships")
            store.respond(call_options)
            for relationship, after in items:
                yield ReadRelationship(relationship, after, read_at)

        return Stream(factory)

    def export_relationships(
        self,
        filter: Filter | None = None,
        *,
        consistency: Consistency,
        page_size: int = 1000,
        cursor: str | None = None,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> Stream[ExportedRelationship]:
        """Yield every relationship (or those matching ``filter``) in
        insertion order, resuming after ``cursor``."""
        Budget(timeout)
        _positive(page_size, "page_size")
        _call_options(call_options)
        if filter is not None and not isinstance(filter, Filter):
            raise InvalidArgumentError("filter: expected Filter")
        _requests.export_request(consistency, filter._to_proto() if filter is not None else None, page_size, None)
        if cursor is not None and (not isinstance(cursor, str) or not cursor):
            raise InvalidArgumentError("cursor: expected nonempty cursor string")
        store = self._store
        match = filter.matches if filter is not None else (lambda _r: True)

        def factory(_owner):
            items, _ = store.page(match, cursor, "export relationships")
            store.respond(call_options)
            for relationship, after in items:
                yield ExportedRelationship(relationship, after)

        return Stream(factory)

    def import_relationships(
        self,
        relationships: Iterable[Relationship],
        *,
        batch_size: int = 1000,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> int:
        """Add every relationship in one transaction, as SpiceDB does: if any
        already exists (or repeats within the import), nothing is added and
        the error is ``AlreadyExistsError``."""
        Budget(timeout)
        _positive(batch_size, "batch_size")
        mutation_retry_policy(call_options, False, "import relationships")
        batch = list(relationships)
        for relationship in batch:
            if not isinstance(relationship, Relationship):
                raise InvalidArgumentError("relationships: expected Relationship values")
            relationship._to_proto()
        self._store.respond(call_options)
        return self._store.import_all(batch)

    def delete_relationships(
        self,
        filter: Filter,
        *,
        limit: int | None = None,
        partial: DeletePartial = DeletePartial.ALLOWED,
        must_match: Sequence[Filter] = (),
        must_not_match: Sequence[Filter] = (),
        metadata: JSONObject | None = None,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> DeleteResult:
        """Delete with SpiceDB's bounded / all-or-nothing semantics."""
        allow_partial = _delete_args(limit, partial)
        Budget(timeout)
        mutation_retry_policy(call_options, False, "delete relationships")
        must_match, must_not_match = tuple(must_match), tuple(must_not_match)
        self._validate_delete(filter, must_match, must_not_match, limit, allow_partial, metadata)
        self._store.respond(call_options)
        return self._store.delete(filter, limit, allow_partial, must_match, must_not_match)

    @staticmethod
    def _validate_delete(filter, must_match, must_not_match, limit, allow_partial, metadata) -> None:
        if not isinstance(filter, Filter):
            raise InvalidArgumentError("filter: expected Filter")
        _requests.delete_relationships_request(
            filter, list(must_match), list(must_not_match), limit, None, allow_partial
        )
        _requests.context_struct(metadata)

    def delete_all_relationships_non_atomic(
        self,
        filter: Filter,
        *,
        page_size: int = 1000,
        must_match: Sequence[Filter] = (),
        must_not_match: Sequence[Filter] = (),
        metadata: JSONObject | None = None,
        on_progress: Callable[[DeleteResult], Any] | None = None,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> DeleteResult:
        """Delete in batches of ``page_size``, reporting each batch to ``on_progress``.

        A failure after an acknowledged batch carries ``delete_progress``, as
        the real client's does.
        """
        Budget(timeout)
        mutation_retry_policy(call_options, False, "delete relationships")
        _positive(page_size, "page_size")
        if on_progress is not None and not callable(on_progress):
            raise InvalidArgumentError("on_progress: expected callable")
        must_match, must_not_match = tuple(must_match), tuple(must_not_match)
        self._validate_delete(filter, must_match, must_not_match, page_size, True, metadata)
        self._store.respond(call_options)
        acknowledged = None
        count = 0
        try:
            while True:
                page = self._store.delete(filter, page_size, True, must_match, must_not_match)
                count += page.deleted_count
                acknowledged = DeleteResult(page.revision, count, page.progress, None)
                if on_progress is not None:
                    on_progress(acknowledged)
                if page.progress is DeleteProgress.COMPLETE:
                    return acknowledged
        except BaseException as error:
            if acknowledged is not None:
                setattr(error, "delete_progress", acknowledged)
            raise

    def read_schema(
        self, *, timeout: float | None = None, call_options: CallOptions | None = None
    ) -> SchemaResult:
        """Return the schema last written, or raise ``NotFoundError`` as SpiceDB does."""
        Budget(timeout)
        _call_options(call_options)
        self._store.respond(call_options)
        with self._store.lock:
            if self._store.schema is None:
                raise SpiceDBError.from_code(grpc.StatusCode.NOT_FOUND, _NO_SCHEMA)
            return SchemaResult(self._store.schema, self._store.token())

    def write_schema(
        self, schema: str, *, timeout: float | None = None, call_options: CallOptions | None = None
    ) -> ZedToken:
        """Store the schema text. It is not compiled or validated."""
        Budget(timeout)
        mutation_retry_policy(call_options, True, "write schema")
        if not isinstance(schema, str):
            raise InvalidArgumentError("schema: expected str")
        self._store.respond(call_options)
        with self._store.lock:
            self._store.schema = schema
            self._store.revision += 1
            return self._store.token()

    def reflect_schema(
        self,
        *,
        consistency: Consistency,
        filters: Sequence[SchemaFilter] = (),
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> ReflectSchemaResult:
        """Return the result registered with :meth:`set_reflect_schema_result`,
        stamped with the current revision; without one, ``NotFoundError``."""
        Budget(timeout)
        _call_options(call_options)
        _requests.reflect_schema_request(consistency, filters)
        self._store.respond(call_options)
        with self._store.lock:
            if self._store.reflection is None:
                raise SpiceDBError.from_code(grpc.StatusCode.NOT_FOUND, _NO_SCHEMA)
            return replace(self._store.reflection, revision=self._store.token())


class AsyncInMemoryClient:
    """In-memory stand-in for :class:`spicedb.aio.SpiceDBClient`."""

    def __init__(self, relationships: Sequence[Relationship] = ()) -> None:
        self._sync = InMemoryClient(relationships)

    def relationships(self) -> list[Relationship]:
        """A snapshot of every stored relationship, in insertion order."""
        return self._sync.relationships()

    def set_check_result(
        self,
        resource: ObjectRef,
        permission: str,
        subject: SubjectRef,
        result: CheckResult | Permissionship,
    ) -> None:
        """Register what ``check``/``check_bulk`` return for this triple."""
        self._sync.set_check_result(resource, permission, subject, result)

    def set_reflect_schema_result(self, result: ReflectSchemaResult) -> None:
        """Register what ``reflect_schema`` returns (stamped with the current revision)."""
        self._sync.set_reflect_schema_result(result)

    def set_response_metadata(
        self,
        headers: Sequence[tuple[str, str | bytes]] = (),
        trailers: Sequence[tuple[str, str | bytes]] = (),
    ) -> None:
        """Set the headers and trailers every operation reports to a
        ``CallOptions(response_metadata=...)`` target."""
        self._sync.set_response_metadata(headers, trailers)

    async def write(
        self,
        transaction: Transaction,
        *,
        metadata: JSONObject | None = None,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> WriteResult:
        """Apply the transaction atomically with SpiceDB semantics."""
        return self._sync.write(transaction, metadata=metadata, timeout=timeout, call_options=call_options)

    async def check(
        self,
        check: PermissionCheck,
        *,
        consistency: Consistency,
        context: JSONObject | None = None,
        tracing: Tracing = Tracing.DISABLED,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> CheckResult:
        """Return the registered result, stamped with the current revision."""
        return self._sync.check(
            check, consistency=consistency, context=context, tracing=tracing, timeout=timeout, call_options=call_options
        )

    async def check_bulk(
        self,
        checks: Sequence[PermissionCheck],
        *,
        consistency: Consistency,
        context: JSONObject | None = None,
        tracing: Tracing = Tracing.DISABLED,
        item_errors: BulkItemErrors = BulkItemErrors.RAISE,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> list[CheckResult]:
        """Return the registered results in input order."""
        return self._sync.check_bulk(
            checks, consistency=consistency, context=context, tracing=tracing,
            item_errors=item_errors, timeout=timeout, call_options=call_options,
        )

    def read_relationships(
        self,
        filter: Filter,
        *,
        consistency: Consistency,
        page_size: int = 1000,
        cursor: str | None = None,
        paging: Paging = Paging.AUTOMATIC,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> AsyncStream[ReadRelationship]:
        """Yield matching relationships (a snapshot) in insertion order."""
        source = self._sync.read_relationships(
            filter, consistency=consistency, page_size=page_size, cursor=cursor,
            paging=paging, timeout=timeout, call_options=call_options,
        )

        async def factory(_owner):
            for item in source:
                yield item

        return AsyncStream(factory)

    def export_relationships(
        self,
        filter: Filter | None = None,
        *,
        consistency: Consistency,
        page_size: int = 1000,
        cursor: str | None = None,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> AsyncStream[ExportedRelationship]:
        """Yield every relationship (or those matching ``filter``)."""
        source = self._sync.export_relationships(
            filter, consistency=consistency, page_size=page_size, cursor=cursor,
            timeout=timeout, call_options=call_options,
        )

        async def factory(_owner):
            for item in source:
                yield item

        return AsyncStream(factory)

    async def import_relationships(
        self,
        relationships: Iterable[Relationship] | AsyncIterable[Relationship],
        *,
        batch_size: int = 1000,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> int:
        """Add every relationship in one transaction; accepts sync or async producers."""
        if isinstance(relationships, AsyncIterable):
            relationships = [r async for r in relationships]
        return self._sync.import_relationships(
            relationships, batch_size=batch_size, timeout=timeout, call_options=call_options
        )

    async def delete_relationships(
        self,
        filter: Filter,
        *,
        limit: int | None = None,
        partial: DeletePartial = DeletePartial.ALLOWED,
        must_match: Sequence[Filter] = (),
        must_not_match: Sequence[Filter] = (),
        metadata: JSONObject | None = None,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> DeleteResult:
        """Delete with SpiceDB's bounded / all-or-nothing semantics."""
        return self._sync.delete_relationships(
            filter, limit=limit, partial=partial, must_match=must_match, must_not_match=must_not_match,
            metadata=metadata, timeout=timeout, call_options=call_options,
        )

    async def delete_all_relationships_non_atomic(
        self,
        filter: Filter,
        *,
        page_size: int = 1000,
        must_match: Sequence[Filter] = (),
        must_not_match: Sequence[Filter] = (),
        metadata: JSONObject | None = None,
        on_progress: Callable[[DeleteResult], Any] | None = None,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> DeleteResult:
        """Delete in batches of ``page_size``; ``on_progress`` may be async."""
        reports: list[DeleteResult] = []
        try:
            result = self._sync.delete_all_relationships_non_atomic(
                filter,
                page_size=page_size,
                must_match=must_match,
                must_not_match=must_not_match,
                metadata=metadata,
                on_progress=reports.append,
                timeout=timeout,
                call_options=call_options,
            )
        finally:
            if on_progress is not None:
                for report in reports:
                    value = on_progress(report)
                    if inspect.isawaitable(value):
                        await value
        return result

    async def read_schema(
        self, *, timeout: float | None = None, call_options: CallOptions | None = None
    ) -> SchemaResult:
        """Return the schema last written, or raise ``NotFoundError`` as SpiceDB does."""
        return self._sync.read_schema(timeout=timeout, call_options=call_options)

    async def write_schema(
        self, schema: str, *, timeout: float | None = None, call_options: CallOptions | None = None
    ) -> ZedToken:
        """Store the schema text. It is not compiled or validated."""
        return self._sync.write_schema(schema, timeout=timeout, call_options=call_options)

    async def reflect_schema(
        self,
        *,
        consistency: Consistency,
        filters: Sequence[SchemaFilter] = (),
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> ReflectSchemaResult:
        """Return the registered reflection, stamped with the current revision."""
        return self._sync.reflect_schema(
            consistency=consistency, filters=filters, timeout=timeout, call_options=call_options
        )
