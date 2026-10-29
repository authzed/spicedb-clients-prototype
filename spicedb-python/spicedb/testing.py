"""In-memory test doubles for the native clients.

:class:`InMemoryClient` stands in for :class:`spicedb.sync.SpiceDBClient` and
:class:`AsyncInMemoryClient` for :class:`spicedb.aio.SpiceDBClient`. Each
implements its client's whole public surface with the same signatures, so
it satisfies :class:`spicedb.protocols.ClientProtocol` (or
:class:`spicedb.protocols.AsyncClientProtocol`), as the real client does, and
code written against that protocol (or a smaller one of your own) accepts
either. ``write``, ``check``, ``check_bulk``, ``lookup_resources``,
``lookup_subjects``, ``read_relationships``,
``delete_relationships``, ``delete_all_relationships_non_atomic``,
``import_relationships``, ``export_relationships``, ``read_schema``,
``write_schema``, ``reflect_schema`` and ``close`` work in memory; every
other operation is recorded, consults the fault hook, and then raises a
``SpiceDBError`` with code UNIMPLEMENTED (a stream raises it on iteration).

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

They do not evaluate a schema or caveats. A check resolves, in order, to
the result registered for that exact question with
:meth:`InMemoryClient.set_check_result`; then to the answer of the resolver
installed with :meth:`InMemoryClient.set_check_resolver` (None defers;
:func:`grant_all` grants everything); then to the stored relationships, where
an unexpired relationship on the resource whose relation is the permission's
name and whose subject matches exactly (or by wildcard, for a direct subject)
grants it, conditionally when it carries a caveat; otherwise
``NO_PERMISSION``. ``lookup_resources`` and ``lookup_subjects`` enumerate the
objects of the requested type found in the stored relationships (as resource
or subject, first appearance first) and yield those the check resolution
grants. ``write_schema`` stores the text without compiling it, and
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

Every call is recorded (:meth:`InMemoryClient.calls`,
:meth:`InMemoryClient.calls_of`) as a :class:`Call` holding its
:class:`spicedb.Operation`, its index among calls of that operation, its
request and its keyword options exactly as passed, so tests assert on what
was sent without a strict mock. Faults are injected at defined points
(:meth:`InMemoryClient.set_fault_hook`, :meth:`InMemoryClient.fail_next`):
before any call, before each import batch, before an import commits and
before each read, export or lookup item, so failure paths need no fake gRPC server.
Build injected errors with :meth:`spicedb.SpiceDBError.from_code`, which
produces exactly what the real client raises for that server failure. A
failed write or import applies nothing. An import-batch fault carries that
batch's relationships (:attr:`Fault.batch`).

Read and export cursors are the decimal count of matching relationships
already yielded, and lookup cursors that of granted resources; :func:`cursor_after` builds one, and the format is stable
for tests. :meth:`InMemoryClient.set_read_schema_revision` makes
``read_schema`` report no revision, as some servers do.
"""

from __future__ import annotations

import enum
import inspect
import threading
from datetime import datetime, timezone
from collections.abc import AsyncIterable, Callable, Iterable, Mapping, Sequence
from dataclasses import dataclass, replace
from types import MappingProxyType
from typing import Any, NoReturn

import grpc

from spicedb import _requests
from spicedb._budget import Budget
from spicedb._client_base import mutation_retry_policy, response_target, write_is_idempotent
from spicedb._streams import AsyncStream, Stream
from spicedb.consistency import Consistency
from spicedb.errors import LocalValidationError, SpiceDBError
from spicedb.options import (
    BulkItemErrors,
    CallOptions,
    DeletePartial,
    LookupDebug,
    LookupPermissionshipFilter,
    Operation,
    Paging,
    Tracing,
    WatchContent,
    Wildcards,
    _option,
)
from spicedb.raw import RawGrpc
from spicedb.token import ZedToken
from spicedb.types import (
    CheckResult,
    CountResult,
    DeleteProgress,
    DeleteResult,
    ExpandResult,
    ExportedRelationship,
    Filter,
    JSONObject,
    LookupResource,
    LookupSubject,
    ObjectRef,
    PartialCaveatInfo,
    PermissionCheck,
    PermissionSetCursor,
    PermissionSetDownload,
    PermissionSetEvent,
    PermissionSetLookup,
    Permissionship,
    PreconditionOperation,
    ReadRelationship,
    ReflectSchemaResult,
    Relationship,
    RelationsResult,
    ResolvedSubject,
    RoaringLookupResourcesResult,
    SchemaDiffResult,
    SchemaFilter,
    SchemaResult,
    SubjectRef,
    Transaction,
    UpdateOperation,
    WatchEvent,
    WriteResult,
)

__all__ = [
    "InMemoryClient",
    "AsyncInMemoryClient",
    "Call",
    "Fault",
    "FaultStage",
    "SchemaRevision",
    "CheckResolver",
    "cursor_after",
    "grant_all",
]

# SpiceDB's default for a delete request without an explicit limit.
SERVER_DEFAULT_DELETE_LIMIT = 1000

_NO_SCHEMA = "No schema has been defined; please call WriteSchema to start"


@dataclass(frozen=True)
class Call:
    """One recorded call of a test double."""

    operation: Operation
    """Which operation was called."""
    index: int
    """Position among calls of this operation, starting at 0."""
    request: Any
    """The operation's subject: the transaction for ``write``, the check (or
    list of checks), the filter for reads, exports and deletes, the list of
    relationships for an import, the schema text for ``write_schema``; None
    for ``read_schema`` and ``reflect_schema``. For an operation without
    in-memory semantics it is the positional argument (``(name, filter)`` for
    ``experimental_register_relationship_counter``), or None when every
    argument is keyword-only."""
    options: Mapping[str, Any]
    """Every keyword argument as passed, defaults included (``consistency``,
    ``cursor``, ``timeout``, ``call_options`` and so on), read-only."""


class FaultStage(enum.Enum):
    """Where in an operation a fault hook is consulted."""

    CALL = "call"
    """Before the operation does anything; a stream raises on iteration."""
    IMPORT_BATCH = "import_batch"
    """Before import batch ``Fault.item`` (0-based); batches have the call's
    ``batch_size``."""
    IMPORT_COMMIT = "import_commit"
    """After every batch, before the import commits."""
    ITEM = "item"
    """Before a read, export or lookup yields item ``Fault.item`` (0-based)."""


@dataclass(frozen=True)
class Fault:
    """The point at which a fault hook is consulted."""

    call: Call
    stage: FaultStage
    item: int = 0
    """The batch index for IMPORT_BATCH, the item index for ITEM (counting
    yielded items), else 0."""
    batch: tuple[Relationship, ...] = ()
    """The relationships in the batch at IMPORT_BATCH; empty otherwise."""


class SchemaRevision(enum.Enum):
    """Whether ``read_schema`` reports a revision."""

    INCLUDED = "included"
    """Report the double's current revision. This is the default."""
    OMITTED = "omitted"
    """Report an empty ``ZedToken("")``, as servers that send no revision with
    a schema read do, so code that falls back to another revision source can
    be tested."""


def cursor_after(n: int) -> str:
    """The cursor that resumes a double's ``read_relationships`` or
    ``export_relationships`` after the first ``n`` matching relationships (in
    insertion order), or its ``lookup_resources`` after the first ``n``
    resources the check resolution grants (counting those a permissionship
    filter skips): the same cursor the double puts on the ``n``-th item.

    The format, the decimal count ``n``, is stable for tests. It is not a
    SpiceDB cursor; never send it to a real server.
    """
    if isinstance(n, bool) or not isinstance(n, int) or n < 0:
        raise LocalValidationError("n: expected non-negative integer")
    return str(n)


FaultHook = Callable[[Fault], "BaseException | None"]

CheckResolver = Callable[[PermissionCheck], "CheckResult | Permissionship | bool | None"]
"""A check resolver: receives each check the double cannot answer from a
registered result (its ``context`` is the call-level context merged with the
check's own, as sent to SpiceDB) and returns a :class:`spicedb.CheckResult`, a
:class:`spicedb.Permissionship`, a bool (True grants, False denies), or None to
defer to the stored relationships."""


def grant_all(check: PermissionCheck) -> Permissionship:
    """A check resolver that grants every check:
    ``double.set_check_resolver(grant_all)``."""
    return Permissionship.HAS_PERMISSION


def _direct(relation: str | None) -> str | None:
    """A subject relation, with ``...`` (direct) as None."""
    return None if relation in (None, "...") else relation


def _expired(relationship: Relationship, now: datetime) -> bool:
    expiration = relationship.expiration
    if expiration is None:
        return False
    if expiration.tzinfo is None:
        expiration = expiration.replace(tzinfo=timezone.utc)
    return expiration <= now


def _resolved(value: Any) -> CheckResult | None:
    """A check resolver's answer as a result, or None to defer."""
    if value is None:
        return None
    if isinstance(value, bool):
        value = Permissionship.HAS_PERMISSION if value else Permissionship.NO_PERMISSION
    if isinstance(value, Permissionship):
        return CheckResult(value, (), ZedToken(""))
    if isinstance(value, CheckResult):
        return value
    raise LocalValidationError(
        f"check resolver: expected CheckResult, Permissionship, bool or None, got {value!r}"
    )


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
        raise LocalValidationError(f"call_options: expected CallOptions, got {call_options!r}")


def _positive(value, field):
    if isinstance(value, bool) or not isinstance(value, int) or value <= 0:
        raise LocalValidationError(f"{field}: expected positive integer")


class _Store:
    def __init__(self, relationships: Sequence[Relationship]) -> None:
        self.lock = threading.RLock()
        self.relationships: dict[Any, Relationship] = {}
        self.checks: dict[Any, CheckResult] = {}
        self.resolver: CheckResolver | None = None
        self.revision = 0
        self.schema: str | None = None
        self.reflection: ReflectSchemaResult | None = None
        self.headers: tuple[tuple[str, str | bytes], ...] = ()
        self.trailers: tuple[tuple[str, str | bytes], ...] = ()
        self.calls: list[Call] = []
        self.counts: dict[Operation, int] = {}
        self.hook: FaultHook | None = None
        self.schema_revision = SchemaRevision.INCLUDED
        for relationship in relationships:
            if not isinstance(relationship, Relationship):
                raise LocalValidationError("relationships: expected Relationship values")
            self.relationships[_key(relationship)] = relationship

    def token(self) -> ZedToken:
        return ZedToken(str(self.revision))

    def begin(self, operation: Operation, request: Any, options: dict[str, Any]) -> tuple[Call, BaseException | None]:
        """Record a call and consult the hook at FaultStage.CALL."""
        with self.lock:
            index = self.counts.get(operation, 0)
            self.counts[operation] = index + 1
            call = Call(operation, index, request, MappingProxyType(dict(options)))
            self.calls.append(call)
        return call, self.fault(call, FaultStage.CALL)

    def fault(
        self, call: Call, stage: FaultStage, item: int = 0, batch: tuple[Relationship, ...] = ()
    ) -> BaseException | None:
        """The hook's error for one point of a call, or None."""
        with self.lock:
            hook = self.hook
        if hook is None:
            return None
        error = hook(Fault(call, stage, item, batch))
        if error is not None and not isinstance(error, BaseException):
            raise LocalValidationError(f"fault hook: expected an exception or None, got {error!r}")
        return error

    def begin_or_raise(self, operation: Operation, request: Any, options: dict[str, Any]) -> Call:
        call, error = self.begin(operation, request, options)
        if error is not None:
            raise error
        return call

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
                    raise LocalValidationError("unknown update operation")
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

    def check(self, check: PermissionCheck, context: JSONObject | None = None) -> CheckResult:
        """Resolve one check: the registered result, then the resolver, then
        the stored relationships; otherwise NO_PERMISSION."""
        with self.lock:
            result = self.checks.get(_check_key(check.resource, check.permission, check.subject))
            resolver = self.resolver
            token = self.token()
        if result is None and resolver is not None:
            merged = _requests._merge_check_context(context, check.context)
            result = _resolved(resolver(replace(check, context=merged)))
        if result is None:
            with self.lock:
                return CheckResult(self.stored(check), (), token)
        return replace(result, checked_at=token)

    def stored(self, check: PermissionCheck) -> Permissionship:
        """What the stored relationships grant: a live relationship on the
        resource whose relation is the permission's name and whose subject
        matches exactly or by wildcard. A caveated one is conditional, since
        the double evaluates no caveat."""
        subject = check.subject
        relation = _direct(subject.relation)
        now = datetime.now(timezone.utc)
        found = Permissionship.NO_PERMISSION
        for r in self.relationships.values():
            if r.resource != check.resource or r.relation != check.permission:
                continue
            stored = r.subject
            if stored.object.object_type != subject.object.object_type or _direct(stored.relation) != relation:
                continue
            if stored.object.object_id not in (subject.object.object_id, "*") or _expired(r, now):
                continue
            if r.caveat is None:
                return Permissionship.HAS_PERMISSION
            found = Permissionship.CONDITIONAL_PERMISSION
        return found

    def known_objects(self, object_type: str) -> list[ObjectRef]:
        """The objects of a type appearing in the stored relationships, as
        resource or subject, in first-appearance order."""
        with self.lock:
            seen: dict[ObjectRef, None] = {}
            for r in self.relationships.values():
                for o in (r.resource, r.subject.object):
                    if o.object_type == object_type:
                        seen.setdefault(o, None)
            return list(seen)

    def granted(self, check: PermissionCheck, context: JSONObject | None):
        """The lookup permissionship and missing context of a check, or None
        when it does not grant."""
        result = self.check(check, context)
        if result.error is not None:
            return None
        if result.permissionship is Permissionship.HAS_PERMISSION:
            return Permissionship.HAS_PERMISSION, None
        if result.permissionship is Permissionship.CONDITIONAL_PERMISSION:
            return Permissionship.CONDITIONAL_PERMISSION, PartialCaveatInfo(tuple(result.missing_context))
        return None


def _cursor_start(cursor: str | None, operation: str) -> int:
    """The number of results a double's cursor has already passed."""
    if cursor is None:
        return 0
    try:
        start = int(cursor)
    except ValueError:
        start = -1
    if start < 0:
        raise SpiceDBError.from_code(grpc.StatusCode.INVALID_ARGUMENT, f"{operation}: invalid cursor")
    return start


def _unimplemented_error(operation: str) -> SpiceDBError:
    """What the doubles raise for an operation they have no semantics for."""
    return SpiceDBError.from_code(
        grpc.StatusCode.UNIMPLEMENTED, f"{operation}: not implemented by the spicedb.testing in-memory double"
    )


def _async_stream(source: Stream[Any]) -> AsyncStream[Any]:
    async def factory(_owner):
        for item in source:
            yield item

    return AsyncStream(factory)


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
        """Register what ``check``/``check_bulk`` (and lookups) return for
        this triple. A registered result takes precedence over the check
        resolver and the stored relationships."""
        if isinstance(result, Permissionship):
            result = CheckResult(result, (), ZedToken(""))
        if not isinstance(result, CheckResult):
            raise LocalValidationError("result: expected CheckResult or Permissionship")
        with self._store.lock:
            self._store.checks[_check_key(resource, permission, subject)] = result

    def set_check_resolver(self, resolver: CheckResolver | None) -> None:
        """Install ``resolver``, consulted for every check (including those
        that lookups make) without a registered result, before the stored
        relationships.

        It receives the :class:`spicedb.PermissionCheck` and returns a
        ``CheckResult``, a ``Permissionship``, a bool, or None to defer to the
        stored relationships, so tests over generated IDs need no per-ID
        registration: ``set_check_resolver(grant_all)`` grants everything.
        It must not call the double. Pass None to remove it.
        """
        if resolver is not None and not callable(resolver):
            raise LocalValidationError("resolver: expected callable or None")
        with self._store.lock:
            self._store.resolver = resolver

    def set_reflect_schema_result(self, result: ReflectSchemaResult) -> None:
        """Register what ``reflect_schema`` returns (stamped with the current revision)."""
        if not isinstance(result, ReflectSchemaResult):
            raise LocalValidationError("result: expected ReflectSchemaResult")
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

    def set_read_schema_revision(self, mode: SchemaRevision) -> None:
        """Select whether ``read_schema`` reports a revision
        (:attr:`SchemaRevision.INCLUDED`, the default) or ``ZedToken("")``
        (:attr:`SchemaRevision.OMITTED`)."""
        if not isinstance(mode, SchemaRevision):
            raise LocalValidationError(f"mode: expected SchemaRevision, got {mode!r}")
        with self._store.lock:
            self._store.schema_revision = mode

    def set_fault_hook(self, hook: Callable[[Fault], BaseException | None] | None) -> None:
        """Install ``hook``, consulted at every :class:`Fault` point.

        An exception it returns is raised by the operation at that point (a
        stream raises it on iteration); returning None lets the operation
        continue. A failed write or import applies nothing. Build errors with
        :meth:`spicedb.SpiceDBError.from_code` to match the real client. The
        hook must not call the double. Pass None to remove it.
        """
        if hook is not None and not callable(hook):
            raise LocalValidationError("hook: expected callable or None")
        with self._store.lock:
            self._store.hook = hook

    def fail_next(self, operation: Operation, error: BaseException) -> None:
        """Make the next call of ``operation`` raise ``error`` before doing
        anything; later calls succeed. Replaces any fault hook."""
        if not isinstance(operation, Operation):
            raise LocalValidationError(f"operation: expected Operation, got {operation!r}")
        if not isinstance(error, BaseException):
            raise LocalValidationError("error: expected an exception")
        lock = threading.Lock()
        pending = [True]

        def hook(fault: Fault) -> BaseException | None:
            if fault.stage is FaultStage.CALL and fault.call.operation is operation:
                with lock:
                    if pending[0]:
                        pending[0] = False
                        return error
            return None

        self.set_fault_hook(hook)

    def calls(self) -> list[Call]:
        """Every recorded call, in order."""
        with self._store.lock:
            return list(self._store.calls)

    def calls_of(self, operation: Operation) -> list[Call]:
        """The recorded calls of one operation, in order."""
        return [call for call in self.calls() if call.operation is operation]

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
        self._store.begin_or_raise(
            Operation.WRITE, transaction, {"metadata": metadata, "timeout": timeout, "call_options": call_options}
        )
        Budget(timeout)
        if not isinstance(transaction, Transaction):
            raise LocalValidationError("transaction: expected Transaction")
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
        """Resolve the check (registered result, then the check resolver, then
        the stored relationships), stamped with the current revision."""
        self._store.begin_or_raise(Operation.CHECK, check, {
            "consistency": consistency, "context": context, "tracing": tracing,
            "timeout": timeout, "call_options": call_options,
        })
        Budget(timeout)
        _call_options(call_options)
        _requests.check_request(check, consistency, context, tracing)
        self._store.respond(call_options)
        return self._store.check(check, context)

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
        """Resolve each check as :meth:`check` does, in input order."""
        self._store.begin_or_raise(Operation.CHECK_BULK, list(checks), {
            "consistency": consistency, "context": context, "tracing": tracing,
            "item_errors": item_errors, "timeout": timeout, "call_options": call_options,
        })
        _option(item_errors, BulkItemErrors)
        Budget(timeout)
        _call_options(call_options)
        _requests.check_bulk_request(consistency, checks, context, tracing)
        self._store.respond(call_options)
        return [self._store.check(c, context) for c in checks]

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
        call, call_error = self._store.begin(Operation.READ_RELATIONSHIPS, filter, {
            "consistency": consistency, "page_size": page_size, "cursor": cursor,
            "paging": paging, "timeout": timeout, "call_options": call_options,
        })
        _option(paging, Paging)
        Budget(timeout)
        _positive(page_size, "page_size")
        _call_options(call_options)
        if not isinstance(filter, Filter):
            raise LocalValidationError("filter: expected Filter")
        _requests.read_relationships_request(filter, consistency, page_size, None)
        if cursor is not None and (not isinstance(cursor, str) or not cursor):
            raise LocalValidationError("cursor: expected nonempty cursor string")
        store = self._store

        def factory(_owner):
            if call_error is not None:
                raise call_error
            items, read_at = store.page(filter.matches, cursor, "read relationships")
            store.respond(call_options)
            for i, (relationship, after) in enumerate(items):
                error = store.fault(call, FaultStage.ITEM, i)
                if error is not None:
                    raise error
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
        call, call_error = self._store.begin(Operation.EXPORT_RELATIONSHIPS, filter, {
            "consistency": consistency, "page_size": page_size, "cursor": cursor,
            "timeout": timeout, "call_options": call_options,
        })
        Budget(timeout)
        _positive(page_size, "page_size")
        _call_options(call_options)
        if filter is not None and not isinstance(filter, Filter):
            raise LocalValidationError("filter: expected Filter")
        _requests.export_request(consistency, filter._to_proto() if filter is not None else None, page_size, None)
        if cursor is not None and (not isinstance(cursor, str) or not cursor):
            raise LocalValidationError("cursor: expected nonempty cursor string")
        store = self._store
        match = filter.matches if filter is not None else (lambda _r: True)

        def factory(_owner):
            if call_error is not None:
                raise call_error
            items, _ = store.page(match, cursor, "export relationships")
            store.respond(call_options)
            for i, (relationship, after) in enumerate(items):
                error = store.fault(call, FaultStage.ITEM, i)
                if error is not None:
                    raise error
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
        batch = list(relationships)
        call = self._store.begin_or_raise(
            Operation.IMPORT_RELATIONSHIPS, batch,
            {"batch_size": batch_size, "timeout": timeout, "call_options": call_options},
        )
        Budget(timeout)
        _positive(batch_size, "batch_size")
        mutation_retry_policy(call_options, False, "import relationships")
        for relationship in batch:
            if not isinstance(relationship, Relationship):
                raise LocalValidationError("relationships: expected Relationship values")
            relationship._to_proto()
        for start in range(0, len(batch), batch_size):
            contents = tuple(batch[start:start + batch_size])
            error = self._store.fault(call, FaultStage.IMPORT_BATCH, start // batch_size, contents)
            if error is not None:
                raise error
        error = self._store.fault(call, FaultStage.IMPORT_COMMIT)
        if error is not None:
            raise error
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
        self._store.begin_or_raise(Operation.DELETE_RELATIONSHIPS, filter, {
            "limit": limit, "partial": partial, "must_match": must_match, "must_not_match": must_not_match,
            "metadata": metadata, "timeout": timeout, "call_options": call_options,
        })
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
            raise LocalValidationError("filter: expected Filter")
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
        self._store.begin_or_raise(Operation.DELETE_ALL_RELATIONSHIPS_NON_ATOMIC, filter, {
            "page_size": page_size, "must_match": must_match, "must_not_match": must_not_match,
            "metadata": metadata, "on_progress": on_progress, "timeout": timeout, "call_options": call_options,
        })
        Budget(timeout)
        mutation_retry_policy(call_options, False, "delete relationships")
        _positive(page_size, "page_size")
        if on_progress is not None and not callable(on_progress):
            raise LocalValidationError("on_progress: expected callable")
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
        """Return the schema last written, or raise ``NotFoundError`` as SpiceDB does.

        The revision is the current one, or ``ZedToken("")`` after
        ``set_read_schema_revision(SchemaRevision.OMITTED)``.
        """
        self._store.begin_or_raise(Operation.READ_SCHEMA, None, {"timeout": timeout, "call_options": call_options})
        Budget(timeout)
        _call_options(call_options)
        self._store.respond(call_options)
        with self._store.lock:
            if self._store.schema is None:
                raise SpiceDBError.from_code(grpc.StatusCode.NOT_FOUND, _NO_SCHEMA)
            if self._store.schema_revision is SchemaRevision.OMITTED:
                return SchemaResult(self._store.schema, ZedToken(""))
            return SchemaResult(self._store.schema, self._store.token())

    def write_schema(
        self, schema: str, *, timeout: float | None = None, call_options: CallOptions | None = None
    ) -> ZedToken:
        """Store the schema text. It is not compiled or validated."""
        self._store.begin_or_raise(Operation.WRITE_SCHEMA, schema, {"timeout": timeout, "call_options": call_options})
        Budget(timeout)
        mutation_retry_policy(call_options, True, "write schema")
        if not isinstance(schema, str):
            raise LocalValidationError("schema: expected str")
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
        self._store.begin_or_raise(Operation.REFLECT_SCHEMA, None, {
            "consistency": consistency, "filters": filters, "timeout": timeout, "call_options": call_options,
        })
        Budget(timeout)
        _call_options(call_options)
        _requests.reflect_schema_request(consistency, filters)
        self._store.respond(call_options)
        with self._store.lock:
            if self._store.reflection is None:
                raise SpiceDBError.from_code(grpc.StatusCode.NOT_FOUND, _NO_SCHEMA)
            return replace(self._store.reflection, revision=self._store.token())

    def connect(self, *, timeout: float | None = None) -> None:
        """Do nothing beyond validating ``timeout``: the double is always ready.

        Not recorded and never faulted, like ``close()``: it is lifecycle,
        not an operation.
        """
        Budget(timeout)

    def close(self) -> None:
        """Do nothing; the double holds no transport and stays usable."""

    def __enter__(self) -> InMemoryClient:
        return self

    def __exit__(self, exc_type, exc_value, traceback) -> None:
        self.close()

    def raw_grpc(self) -> RawGrpc:
        """Raise an UNIMPLEMENTED ``SpiceDBError``: the double has no channel."""
        raise _unimplemented_error("raw_grpc")

    def _unimplemented(self, operation: Operation, request: Any, options: dict[str, Any]) -> NoReturn:
        """Record the call, consult the fault hook, validate the shared
        options, then fail as unimplemented."""
        self._store.begin_or_raise(operation, request, options)
        Budget(options["timeout"])
        _call_options(options["call_options"])
        raise _unimplemented_error(operation.value)

    def _unimplemented_stream(self, operation: Operation, request: Any, options: dict[str, Any]) -> Stream[Any]:
        """Like :meth:`_unimplemented`, failing when the stream is iterated."""
        _, call_error = self._store.begin(operation, request, options)
        Budget(options["timeout"])
        _call_options(options["call_options"])

        def factory(_owner):
            if call_error is not None:
                raise call_error
            raise _unimplemented_error(operation.value)
            yield  # pragma: no cover - makes this a generator

        return Stream(factory)

    # The remaining operations have no in-memory semantics. Each is recorded
    # (its positional arguments as the request, every keyword argument in
    # options), consults the fault hook, and fails with UNIMPLEMENTED.

    def expand_permission_tree(
        self,
        *,
        resource: ObjectRef,
        permission: str,
        consistency: Consistency,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> ExpandResult:
        """Not implemented: recorded, then raises UNIMPLEMENTED."""
        self._unimplemented(Operation.EXPAND_PERMISSION_TREE, None, {
            "resource": resource, "permission": permission, "consistency": consistency,
            "timeout": timeout, "call_options": call_options,
        })

    def computable_permissions(
        self,
        *,
        definition_name: str,
        relation_name: str,
        consistency: Consistency,
        definition_name_filter: str | None = None,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> RelationsResult:
        """Not implemented: recorded, then raises UNIMPLEMENTED."""
        self._unimplemented(Operation.COMPUTABLE_PERMISSIONS, None, {
            "definition_name": definition_name, "relation_name": relation_name, "consistency": consistency,
            "definition_name_filter": definition_name_filter, "timeout": timeout, "call_options": call_options,
        })

    def dependent_relations(
        self,
        *,
        definition_name: str,
        permission_name: str,
        consistency: Consistency,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> RelationsResult:
        """Not implemented: recorded, then raises UNIMPLEMENTED."""
        self._unimplemented(Operation.DEPENDENT_RELATIONS, None, {
            "definition_name": definition_name, "permission_name": permission_name, "consistency": consistency,
            "timeout": timeout, "call_options": call_options,
        })

    def diff_schema(
        self,
        comparison_schema: str,
        *,
        consistency: Consistency,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> SchemaDiffResult:
        """Not implemented: recorded, then raises UNIMPLEMENTED."""
        self._unimplemented(Operation.DIFF_SCHEMA, comparison_schema, {
            "consistency": consistency, "timeout": timeout, "call_options": call_options,
        })

    def experimental_register_relationship_counter(
        self, name: str, filter: Filter, *, timeout: float | None = None, call_options: CallOptions | None = None
    ) -> None:
        """Not implemented: recorded (request ``(name, filter)``), then raises UNIMPLEMENTED."""
        self._unimplemented(Operation.EXPERIMENTAL_REGISTER_RELATIONSHIP_COUNTER, (name, filter), {
            "timeout": timeout, "call_options": call_options,
        })

    def experimental_unregister_relationship_counter(
        self, name: str, *, timeout: float | None = None, call_options: CallOptions | None = None
    ) -> None:
        """Not implemented: recorded, then raises UNIMPLEMENTED."""
        self._unimplemented(Operation.EXPERIMENTAL_UNREGISTER_RELATIONSHIP_COUNTER, name, {
            "timeout": timeout, "call_options": call_options,
        })

    def experimental_count_relationships(
        self, name: str, *, timeout: float | None = None, call_options: CallOptions | None = None
    ) -> CountResult:
        """Not implemented: recorded, then raises UNIMPLEMENTED."""
        self._unimplemented(Operation.EXPERIMENTAL_COUNT_RELATIONSHIPS, name, {
            "timeout": timeout, "call_options": call_options,
        })

    def experimental_count_relationships_by_filter(
        self, filter: Filter, *, timeout: float | None = None, call_options: CallOptions | None = None
    ) -> CountResult:
        """Not implemented: recorded, then raises UNIMPLEMENTED."""
        self._unimplemented(Operation.EXPERIMENTAL_COUNT_RELATIONSHIPS_BY_FILTER, filter, {
            "timeout": timeout, "call_options": call_options,
        })

    def experimental_roaring_lookup_resources(
        self,
        *,
        resource_type: str,
        permission: str,
        subject: SubjectRef,
        consistency: Consistency,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> RoaringLookupResourcesResult:
        """Not implemented: recorded, then raises UNIMPLEMENTED."""
        self._unimplemented(Operation.EXPERIMENTAL_ROARING_LOOKUP_RESOURCES, None, {
            "resource_type": resource_type, "permission": permission, "subject": subject,
            "consistency": consistency, "timeout": timeout, "call_options": call_options,
        })

    def lookup_resources(
        self,
        *,
        resource_type: str,
        permission: str,
        subject: SubjectRef,
        consistency: Consistency,
        context: JSONObject | None = None,
        permissionship_filter: LookupPermissionshipFilter = LookupPermissionshipFilter.ALL,
        limit: int | None = None,
        page_size: int = 1000,
        debug: LookupDebug = LookupDebug.DISABLED,
        cursor: str | None = None,
        paging: Paging = Paging.AUTOMATIC,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> Stream[LookupResource]:
        """Yield the objects of ``resource_type`` found in the stored
        relationships (as resource or subject, in first-appearance order) that
        the check resolution grants, conditional ones as CONDITIONAL_PERMISSION.

        ``permissionship_filter``, ``limit`` and ``cursor`` behave as on the
        real client; each item's cursor is the decimal count of granted
        resources up to and including it (see :func:`cursor_after`).
        """
        call, call_error = self._store.begin(Operation.LOOKUP_RESOURCES, None, {
            "resource_type": resource_type, "permission": permission, "subject": subject,
            "consistency": consistency, "context": context, "permissionship_filter": permissionship_filter,
            "limit": limit, "page_size": page_size, "debug": debug, "cursor": cursor, "paging": paging,
            "timeout": timeout, "call_options": call_options,
        })
        _option(paging, Paging)
        _option(permissionship_filter, LookupPermissionshipFilter)
        _option(debug, LookupDebug)
        Budget(timeout)
        _positive(page_size, "page_size")
        if limit is not None:
            _positive(limit, "limit")
        _call_options(call_options)
        if not isinstance(subject, SubjectRef):
            raise LocalValidationError("subject: expected SubjectRef")
        _requests.lookup_resources_request(
            resource_type, permission, subject._to_proto(), _requests.context_struct(context), consistency,
            page_size, None,
        )
        if cursor is not None and (not isinstance(cursor, str) or not cursor):
            raise LocalValidationError("cursor: expected nonempty cursor string")
        has_only = permissionship_filter is LookupPermissionshipFilter.HAS_PERMISSION_ONLY
        store = self._store

        def factory(_owner):
            if call_error is not None:
                raise call_error
            start = _cursor_start(cursor, "lookup resources")
            store.respond(call_options)
            token = store.token()
            position = yielded = 0
            for obj in store.known_objects(resource_type):
                if obj.object_id == "*":
                    continue
                granted = store.granted(PermissionCheck(obj, permission, subject), context)
                if granted is None:
                    continue
                position += 1
                if position <= start or (has_only and granted[0] is not Permissionship.HAS_PERMISSION):
                    continue
                if limit is not None and yielded >= limit:
                    return
                error = store.fault(call, FaultStage.ITEM, yielded)
                if error is not None:
                    raise error
                yielded += 1
                yield LookupResource(obj.object_id, granted[0], granted[1], token, str(position))

        return Stream(factory)

    def lookup_subjects(
        self,
        *,
        resource: ObjectRef,
        permission: str,
        subject_type: str,
        consistency: Consistency,
        subject_relation: str | None = None,
        context: JSONObject | None = None,
        page_size: int | None = None,
        limit: int | None = None,
        wildcards: Wildcards = Wildcards.DEFAULT,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> Stream[LookupSubject]:
        """Yield the objects of ``subject_type`` found in the stored
        relationships (in first-appearance order) that the check resolution
        grants as subjects (with ``subject_relation``), conditional ones as
        CONDITIONAL_PERMISSION. A stored wildcard subject (``*``) is yielded
        too unless ``wildcards=Wildcards.EXCLUDE``. ``limit`` bounds the
        results; ``page_size`` is validated and otherwise ignored.
        """
        call, call_error = self._store.begin(Operation.LOOKUP_SUBJECTS, None, {
            "resource": resource, "permission": permission, "subject_type": subject_type,
            "consistency": consistency, "subject_relation": subject_relation, "context": context,
            "page_size": page_size, "limit": limit, "wildcards": wildcards,
            "timeout": timeout, "call_options": call_options,
        })
        exclude_wildcards = _option(wildcards, Wildcards) == Wildcards.EXCLUDE.value
        Budget(timeout)
        if page_size is not None:
            _positive(page_size, "page_size")
        if limit is not None:
            _positive(limit, "limit")
        _call_options(call_options)
        if not isinstance(resource, ObjectRef):
            raise LocalValidationError("resource: expected ObjectRef")
        _requests.lookup_subjects_request(
            resource, permission, subject_type, subject_relation or "", _requests.context_struct(context),
            consistency,
        )
        store = self._store

        def factory(_owner):
            if call_error is not None:
                raise call_error
            store.respond(call_options)
            token = store.token()
            yielded = 0
            for obj in store.known_objects(subject_type):
                if obj.object_id == "*" and (exclude_wildcards or subject_relation):
                    continue
                check = PermissionCheck(resource, permission, SubjectRef(obj, subject_relation or None))
                granted = store.granted(check, context)
                if granted is None:
                    continue
                if limit is not None and yielded >= limit:
                    return
                error = store.fault(call, FaultStage.ITEM, yielded)
                if error is not None:
                    raise error
                yielded += 1
                yield LookupSubject(ResolvedSubject(obj.object_id, granted[0], granted[1]), (), token)

        return Stream(factory)

    def watch(
        self,
        object_types: Sequence[str] = (),
        *,
        start_revision: ZedToken | None = None,
        content: Sequence[WatchContent] = (WatchContent.RELATIONSHIPS,),
        filters: Sequence[Filter] = (),
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> Stream[WatchEvent]:
        """Not implemented: recorded; iterating raises UNIMPLEMENTED."""
        return self._unimplemented_stream(Operation.WATCH, object_types, {
            "start_revision": start_revision, "content": content, "filters": filters,
            "timeout": timeout, "call_options": call_options,
        })

    def watch_permission_sets(
        self,
        *,
        start_revision: ZedToken | None = None,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> Stream[PermissionSetEvent]:
        """Not implemented: recorded; iterating raises UNIMPLEMENTED."""
        return self._unimplemented_stream(Operation.WATCH_PERMISSION_SETS, None, {
            "start_revision": start_revision, "timeout": timeout, "call_options": call_options,
        })

    def lookup_permission_sets(
        self,
        *,
        limit: int = 1000,
        at_revision: ZedToken | None = None,
        cursor: PermissionSetCursor | None = None,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> Stream[PermissionSetLookup]:
        """Not implemented: recorded; iterating raises UNIMPLEMENTED."""
        return self._unimplemented_stream(Operation.LOOKUP_PERMISSION_SETS, None, {
            "limit": limit, "at_revision": at_revision, "cursor": cursor,
            "timeout": timeout, "call_options": call_options,
        })

    def download_permission_sets(
        self,
        *,
        at_revision: ZedToken | None = None,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> PermissionSetDownload:
        """Not implemented: recorded, then raises UNIMPLEMENTED."""
        self._unimplemented(Operation.DOWNLOAD_PERMISSION_SETS, None, {
            "at_revision": at_revision, "timeout": timeout, "call_options": call_options,
        })


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
        """See :meth:`InMemoryClient.set_check_result`."""
        self._sync.set_check_result(resource, permission, subject, result)

    def set_check_resolver(self, resolver: CheckResolver | None) -> None:
        """See :meth:`InMemoryClient.set_check_resolver`. The resolver is a
        plain (not async) callable."""
        self._sync.set_check_resolver(resolver)

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

    def set_read_schema_revision(self, mode: SchemaRevision) -> None:
        """See :meth:`InMemoryClient.set_read_schema_revision`."""
        self._sync.set_read_schema_revision(mode)

    def set_fault_hook(self, hook: Callable[[Fault], BaseException | None] | None) -> None:
        """See :meth:`InMemoryClient.set_fault_hook`."""
        self._sync.set_fault_hook(hook)

    def fail_next(self, operation: Operation, error: BaseException) -> None:
        """See :meth:`InMemoryClient.fail_next`."""
        self._sync.fail_next(operation, error)

    def calls(self) -> list[Call]:
        """Every recorded call, in order."""
        return self._sync.calls()

    def calls_of(self, operation: Operation) -> list[Call]:
        """The recorded calls of one operation, in order."""
        return self._sync.calls_of(operation)

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
        """See :meth:`InMemoryClient.check`."""
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
        """See :meth:`InMemoryClient.check_bulk`."""
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
        return _async_stream(self._sync.read_relationships(
            filter, consistency=consistency, page_size=page_size, cursor=cursor,
            paging=paging, timeout=timeout, call_options=call_options,
        ))

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
        return _async_stream(self._sync.export_relationships(
            filter, consistency=consistency, page_size=page_size, cursor=cursor,
            timeout=timeout, call_options=call_options,
        ))

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

    async def connect(self, *, timeout: float | None = None) -> None:
        """Do nothing beyond validating ``timeout``: the double is always ready."""
        self._sync.connect(timeout=timeout)

    async def close(self) -> None:
        """Do nothing; the double holds no transport and stays usable."""

    async def __aenter__(self) -> AsyncInMemoryClient:
        return self

    async def __aexit__(self, exc_type, exc_value, traceback) -> None:
        await self.close()

    async def raw_grpc(self) -> RawGrpc:
        """Raise an UNIMPLEMENTED ``SpiceDBError``: the double has no channel."""
        return self._sync.raw_grpc()

    # The remaining operations have no in-memory semantics; see
    # InMemoryClient: each is recorded, consults the fault hook, and fails
    # with UNIMPLEMENTED (streams on iteration).

    async def expand_permission_tree(
        self,
        *,
        resource: ObjectRef,
        permission: str,
        consistency: Consistency,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> ExpandResult:
        """Not implemented: recorded, then raises UNIMPLEMENTED."""
        return self._sync.expand_permission_tree(
            resource=resource, permission=permission, consistency=consistency,
            timeout=timeout, call_options=call_options,
        )

    async def computable_permissions(
        self,
        *,
        definition_name: str,
        relation_name: str,
        consistency: Consistency,
        definition_name_filter: str | None = None,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> RelationsResult:
        """Not implemented: recorded, then raises UNIMPLEMENTED."""
        return self._sync.computable_permissions(
            definition_name=definition_name, relation_name=relation_name, consistency=consistency,
            definition_name_filter=definition_name_filter, timeout=timeout, call_options=call_options,
        )

    async def dependent_relations(
        self,
        *,
        definition_name: str,
        permission_name: str,
        consistency: Consistency,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> RelationsResult:
        """Not implemented: recorded, then raises UNIMPLEMENTED."""
        return self._sync.dependent_relations(
            definition_name=definition_name, permission_name=permission_name, consistency=consistency,
            timeout=timeout, call_options=call_options,
        )

    async def diff_schema(
        self,
        comparison_schema: str,
        *,
        consistency: Consistency,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> SchemaDiffResult:
        """Not implemented: recorded, then raises UNIMPLEMENTED."""
        return self._sync.diff_schema(
            comparison_schema, consistency=consistency, timeout=timeout, call_options=call_options
        )

    async def experimental_register_relationship_counter(
        self, name: str, filter: Filter, *, timeout: float | None = None, call_options: CallOptions | None = None
    ) -> None:
        """Not implemented: recorded (request ``(name, filter)``), then raises UNIMPLEMENTED."""
        self._sync.experimental_register_relationship_counter(
            name, filter, timeout=timeout, call_options=call_options
        )

    async def experimental_unregister_relationship_counter(
        self, name: str, *, timeout: float | None = None, call_options: CallOptions | None = None
    ) -> None:
        """Not implemented: recorded, then raises UNIMPLEMENTED."""
        self._sync.experimental_unregister_relationship_counter(name, timeout=timeout, call_options=call_options)

    async def experimental_count_relationships(
        self, name: str, *, timeout: float | None = None, call_options: CallOptions | None = None
    ) -> CountResult:
        """Not implemented: recorded, then raises UNIMPLEMENTED."""
        return self._sync.experimental_count_relationships(name, timeout=timeout, call_options=call_options)

    async def experimental_count_relationships_by_filter(
        self, filter: Filter, *, timeout: float | None = None, call_options: CallOptions | None = None
    ) -> CountResult:
        """Not implemented: recorded, then raises UNIMPLEMENTED."""
        return self._sync.experimental_count_relationships_by_filter(
            filter, timeout=timeout, call_options=call_options
        )

    async def experimental_roaring_lookup_resources(
        self,
        *,
        resource_type: str,
        permission: str,
        subject: SubjectRef,
        consistency: Consistency,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> RoaringLookupResourcesResult:
        """Not implemented: recorded, then raises UNIMPLEMENTED."""
        return self._sync.experimental_roaring_lookup_resources(
            resource_type=resource_type, permission=permission, subject=subject,
            consistency=consistency, timeout=timeout, call_options=call_options,
        )

    def lookup_resources(
        self,
        *,
        resource_type: str,
        permission: str,
        subject: SubjectRef,
        consistency: Consistency,
        context: JSONObject | None = None,
        permissionship_filter: LookupPermissionshipFilter = LookupPermissionshipFilter.ALL,
        limit: int | None = None,
        page_size: int = 1000,
        debug: LookupDebug = LookupDebug.DISABLED,
        cursor: str | None = None,
        paging: Paging = Paging.AUTOMATIC,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> AsyncStream[LookupResource]:
        """Not implemented: recorded; iterating raises UNIMPLEMENTED."""
        return _async_stream(self._sync.lookup_resources(
            resource_type=resource_type, permission=permission, subject=subject, consistency=consistency,
            context=context, permissionship_filter=permissionship_filter, limit=limit, page_size=page_size,
            debug=debug, cursor=cursor, paging=paging, timeout=timeout, call_options=call_options,
        ))

    def lookup_subjects(
        self,
        *,
        resource: ObjectRef,
        permission: str,
        subject_type: str,
        consistency: Consistency,
        subject_relation: str | None = None,
        context: JSONObject | None = None,
        page_size: int | None = None,
        limit: int | None = None,
        wildcards: Wildcards = Wildcards.DEFAULT,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> AsyncStream[LookupSubject]:
        """Not implemented: recorded; iterating raises UNIMPLEMENTED."""
        return _async_stream(self._sync.lookup_subjects(
            resource=resource, permission=permission, subject_type=subject_type, consistency=consistency,
            subject_relation=subject_relation, context=context, page_size=page_size, limit=limit,
            wildcards=wildcards, timeout=timeout, call_options=call_options,
        ))

    def watch(
        self,
        object_types: Sequence[str] = (),
        *,
        start_revision: ZedToken | None = None,
        content: Sequence[WatchContent] = (WatchContent.RELATIONSHIPS,),
        filters: Sequence[Filter] = (),
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> AsyncStream[WatchEvent]:
        """Not implemented: recorded; iterating raises UNIMPLEMENTED."""
        return _async_stream(self._sync.watch(
            object_types, start_revision=start_revision, content=content, filters=filters,
            timeout=timeout, call_options=call_options,
        ))

    def watch_permission_sets(
        self,
        *,
        start_revision: ZedToken | None = None,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> AsyncStream[PermissionSetEvent]:
        """Not implemented: recorded; iterating raises UNIMPLEMENTED."""
        return _async_stream(self._sync.watch_permission_sets(
            start_revision=start_revision, timeout=timeout, call_options=call_options
        ))

    def lookup_permission_sets(
        self,
        *,
        limit: int = 1000,
        at_revision: ZedToken | None = None,
        cursor: PermissionSetCursor | None = None,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> AsyncStream[PermissionSetLookup]:
        """Not implemented: recorded; iterating raises UNIMPLEMENTED."""
        return _async_stream(self._sync.lookup_permission_sets(
            limit=limit, at_revision=at_revision, cursor=cursor, timeout=timeout, call_options=call_options
        ))

    async def download_permission_sets(
        self,
        *,
        at_revision: ZedToken | None = None,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> PermissionSetDownload:
        """Not implemented: recorded, then raises UNIMPLEMENTED."""
        return self._sync.download_permission_sets(
            at_revision=at_revision, timeout=timeout, call_options=call_options
        )
