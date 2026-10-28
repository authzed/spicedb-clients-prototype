"""In-memory test doubles for the relationship and check operations.

:class:`InMemoryClient` stands in for :class:`spicedb.sync.SpiceDBClient` and
:class:`AsyncInMemoryClient` for :class:`spicedb.aio.SpiceDBClient`. Their
``write``, ``check``, ``check_bulk``, ``read_relationships``,
``delete_relationships`` and ``delete_all_relationships_non_atomic`` methods
have the same signatures as the real clients', so code written against a
``Protocol`` naming those methods accepts either.

They apply SpiceDB's relationship semantics -- filter matching
(:meth:`spicedb.Filter.matches`), write preconditions, CREATE conflicts
(``AlreadyExistsError``), atomic transactions, bounded and all-or-nothing
deletes -- and raise the same typed errors, built with
:meth:`spicedb.SpiceDBError.from_code`. They do not evaluate a schema:
checks return results registered with :meth:`set_check_result` and default
to ``NO_PERMISSION``. Consistency, timeout and call options are accepted and
ignored. Both are safe for use from multiple threads.
"""

from __future__ import annotations

import threading
from collections.abc import Callable, Sequence
from dataclasses import replace
from typing import Any

import grpc

from spicedb._streams import AsyncStream, Stream
from spicedb.consistency import Consistency
from spicedb.errors import InvalidArgumentError, SpiceDBError
from spicedb.options import BulkItemErrors, CallOptions, DeletePartial, Tracing, _option
from spicedb.token import ZedToken
from spicedb.types import (
    CheckResult,
    DeleteProgress,
    DeleteResult,
    Filter,
    JSONObject,
    ObjectRef,
    PermissionCheck,
    Permissionship,
    PreconditionOperation,
    ReadRelationship,
    Relationship,
    SubjectRef,
    Transaction,
    UpdateOperation,
    WriteResult,
)

__all__ = ["InMemoryClient", "AsyncInMemoryClient"]

# SpiceDB's default for a delete request without an explicit limit.
SERVER_DEFAULT_DELETE_LIMIT = 1000


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


class _Store:
    def __init__(self, relationships: Sequence[Relationship]) -> None:
        self.lock = threading.RLock()
        self.relationships: dict[Any, Relationship] = {}
        self.checks: dict[Any, CheckResult] = {}
        self.revision = 0
        for relationship in relationships:
            if not isinstance(relationship, Relationship):
                raise InvalidArgumentError("relationships: expected Relationship values")
            self.relationships[_key(relationship)] = relationship

    def token(self) -> ZedToken:
        return ZedToken(str(self.revision))

    def matches_any(self, filter: Filter) -> bool:
        return any(filter.matches(r) for r in self.relationships.values())

    def check_preconditions(self, preconditions) -> None:
        for precondition in preconditions:
            matched = self.matches_any(precondition.filter)
            wanted = precondition.operation is PreconditionOperation.MUST_MATCH
            if matched != wanted:
                raise _precondition_error(str(precondition))

    def write(self, transaction: Transaction) -> WriteResult:
        if not isinstance(transaction, Transaction):
            raise InvalidArgumentError("transaction: expected Transaction")
        with self.lock:
            if len(transaction) == 0:
                raise SpiceDBError.from_code(grpc.StatusCode.INVALID_ARGUMENT, "transaction requires updates")
            self.check_preconditions(transaction.preconditions)
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
                    raise SpiceDBError.from_code(grpc.StatusCode.INVALID_ARGUMENT, "unknown update operation")
            self.relationships = staged
            self.revision += 1
            return WriteResult(self.token())

    def read(self, filter: Filter) -> list[ReadRelationship]:
        if not isinstance(filter, Filter):
            raise InvalidArgumentError("filter: expected Filter")
        filter._to_proto()
        with self.lock:
            read_at = self.token()
            matched = [r for r in self.relationships.values() if filter.matches(r)]
        return [ReadRelationship(r, str(i + 1), read_at) for i, r in enumerate(matched)]

    def delete(self, filter, limit, allow_partial, must_match, must_not_match) -> DeleteResult:
        if not isinstance(filter, Filter):
            raise InvalidArgumentError("filter: expected Filter")
        filter._to_proto()
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
        if not isinstance(check, PermissionCheck):
            raise InvalidArgumentError("check: expected PermissionCheck")
        with self.lock:
            result = self.checks.get(_check_key(check.resource, check.permission, check.subject))
            token = self.token()
        if result is None:
            return CheckResult(Permissionship.NO_PERMISSION, (), token)
        return replace(result, checked_at=token)


def _delete_args(limit, partial):
    allow_partial = _option(partial, DeletePartial) == "allowed"
    if allow_partial or limit is not None:
        if isinstance(limit, bool) or not isinstance(limit, int) or limit <= 0:
            raise InvalidArgumentError("limit: expected positive integer")
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

    def write(
        self,
        transaction: Transaction,
        *,
        metadata: JSONObject | None = None,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> WriteResult:
        """Apply the transaction atomically with SpiceDB semantics."""
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
        return [self._store.check(c) for c in checks]

    def read_relationships(
        self,
        filter: Filter,
        *,
        consistency: Consistency,
        page_size: int = 1000,
        cursor: str | None = None,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> Stream[ReadRelationship]:
        """Yield matching relationships (a snapshot) in insertion order."""
        items = self._store.read(filter)
        if cursor is not None:
            items = items[int(cursor):]

        def factory(_owner):
            yield from items

        return Stream(factory)

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
        return self._store.delete(filter, limit, allow_partial, tuple(must_match), tuple(must_not_match))

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
        """Delete in batches of ``page_size``, reporting each batch to ``on_progress``."""
        _delete_args(page_size, DeletePartial.ALLOWED)
        count = 0
        while True:
            page = self._store.delete(filter, page_size, True, tuple(must_match), tuple(must_not_match))
            count += page.deleted_count
            result = DeleteResult(page.revision, count, page.progress, None)
            if on_progress is not None:
                on_progress(result)
            if page.progress is DeleteProgress.COMPLETE:
                return result


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

    async def write(
        self,
        transaction: Transaction,
        *,
        metadata: JSONObject | None = None,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> WriteResult:
        """Apply the transaction atomically with SpiceDB semantics."""
        return self._sync.write(transaction)

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
        return self._sync.check(check, consistency=consistency)

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
        return self._sync.check_bulk(checks, consistency=consistency, item_errors=item_errors)

    def read_relationships(
        self,
        filter: Filter,
        *,
        consistency: Consistency,
        page_size: int = 1000,
        cursor: str | None = None,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> AsyncStream[ReadRelationship]:
        """Yield matching relationships (a snapshot) in insertion order."""
        items = list(self._sync.read_relationships(filter, consistency=consistency, cursor=cursor))

        async def factory(_owner):
            for item in items:
                yield item

        return AsyncStream(factory)

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
            filter, limit=limit, partial=partial, must_match=must_match, must_not_match=must_not_match
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
        import inspect

        reports: list[DeleteResult] = []
        result = self._sync.delete_all_relationships_non_atomic(
            filter,
            page_size=page_size,
            must_match=must_match,
            must_not_match=must_not_match,
            on_progress=reports.append,
        )
        if on_progress is not None:
            for report in reports:
                value = on_progress(report)
                if inspect.isawaitable(value):
                    await value
        return result
