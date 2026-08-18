"""Sync client: wiring, retry, and lifecycle.

Request-building and response-mapping are covered once in test_requests.py and
test_mapping.py -- this file only asserts the sync client drives them correctly.
"""

from unittest import mock

import grpc
import pytest

from spicedb import Filter, Permissionship, Relationship
from spicedb.consistency import full
from spicedb.errors import PermissionDeniedError, UnavailableError
from spicedb.sync import SpiceDBClient


class _SyncRpcError(grpc.RpcError):
    def __init__(self, code, details="boom"):
        self._code, self._details = code, details

    def code(self):
        return self._code

    def details(self):
        return self._details


@pytest.fixture
def make_client():
    """Construct SpiceDBClient(s) for a test and close them all on teardown.

    A factory (not a plain fixture) because several tests vary
    ``max_retries`` -- a fixed fixture would force those to keep
    constructing by hand, which is how the leaked-channel debt this fixture
    fixes originally persisted.
    """
    created = []

    def _make(**kw):
        c = SpiceDBClient("localhost:50051", token="t", insecure=True, **kw)
        c._ensure_channel()
        created.append(c)
        return c

    yield _make
    for c in created:
        c.close()


def test_construction_opens_no_channel():
    c = SpiceDBClient("localhost:50051", token="t", insecure=True)
    assert c._channel is None


def test_close_before_any_call_is_a_noop():
    SpiceDBClient("localhost:50051", token="t", insecure=True).close()  # must not raise


def test_context_manager_closes_the_channel():
    c = SpiceDBClient("localhost:50051", token="t", insecure=True)
    with c as entered:
        assert entered is c
        entered._ensure_channel()
        assert entered._channel is not None
    assert c._channel is None


def test_check_permission_returns_check_result(make_client):
    from authzed.api.v1 import core_pb2
    from authzed.api.v1 import permission_service_pb2 as psp

    c = make_client()
    resp = psp.CheckBulkPermissionsResponse(
        checked_at=core_pb2.ZedToken(token="deadbeef"),
        pairs=[
            psp.CheckBulkPermissionsPair(
                item=psp.CheckBulkPermissionsResponseItem(
                    permissionship=psp.CheckPermissionResponse.PERMISSIONSHIP_HAS_PERMISSION
                )
            )
        ]
    )
    with mock.patch.object(c._permissions, "CheckBulkPermissions", return_value=resp):
        rel = Relationship.from_triple("document:readme", "view", "user:jimmy")
        result = c.check_permission(full(), rel)
        assert result.permissionship == Permissionship.HAS_PERMISSION
        assert result.has_permission is True
        assert result.checked_at == "deadbeef"


def test_check_any_and_check_all_do_not_count_conditional(make_client):
    """Sync-flavor counterpart of
    tests/test_client.py::TestCheckPermissionReturnsCheckResult::test_check_any_and_check_all_do_not_count_conditional.

    check_any/check_all are implemented independently in each flavor's
    client.py (not shared via _mapping.py), and test_parity.py only compares
    signatures -- it would not catch the sync flavor alone regressing to
    counting a Conditional result as granted. This must stay in lockstep
    with the aio-flavor test above.
    """
    from authzed.api.v1 import core_pb2
    from authzed.api.v1 import permission_service_pb2 as psp

    c = make_client()
    resp = psp.CheckBulkPermissionsResponse(
        checked_at=core_pb2.ZedToken(token="deadbeef"),
        pairs=[
            psp.CheckBulkPermissionsPair(
                item=psp.CheckBulkPermissionsResponseItem(
                    permissionship=psp.CheckPermissionResponse.PERMISSIONSHIP_CONDITIONAL_PERMISSION
                )
            )
        ]
    )
    with mock.patch.object(c._permissions, "CheckBulkPermissions", return_value=resp):
        rel = Relationship.from_triple(
            "document:readme", "conditional_view", "user:jimmy"
        )
        assert c.check_any(full(), rel) is False
        assert c.check_all(full(), rel) is False


def test_check_all_with_zero_relationships_returns_false(make_client):
    """Sync-flavor counterpart of
    tests/test_client.py::TestCheckPermissionReturnsCheckResult::
    test_check_all_with_zero_relationships_returns_false.

    Python's builtin all() is vacuously True over an empty iterable, so
    check_all(cs, "edit") with no relationships would otherwise return True
    -- a fail-open authorization gate for a call site like
    check_all(cs, "edit", *docs_to_rels(docs)) whenever the derived
    relationship collection comes up empty. Root DESIGN.md: "An aggregate
    over zero checks is not a grant." check_any/check_all are implemented
    independently in each flavor's client.py and test_parity.py only
    compares signatures, so this must stay in lockstep with the aio-flavor
    test above.
    """
    from authzed.api.v1 import core_pb2
    from authzed.api.v1 import permission_service_pb2 as psp

    c = make_client()
    resp = psp.CheckBulkPermissionsResponse(
        checked_at=core_pb2.ZedToken(token="deadbeef"),
        pairs=[],
    )
    with mock.patch.object(c._permissions, "CheckBulkPermissions", return_value=resp):
        assert c.check_all(full()) is False


def test_transient_error_is_retried_then_succeeds(make_client):
    """Guards the Task 1 is_transient fix -- without it this retries zero times.

    Uses a read (ReadSchema), not WriteRelationships: mutations are no
    longer retried at all (see test_mutation_transient_error_is_attempted_once_only
    below) -- retrying a WriteRelationships whose response was lost, but
    which in fact committed, would surface ALREADY_EXISTS/FAILED_PRECONDITION
    for a write that succeeded. DESIGN.md, "Automatic retry is for
    idempotent operations only".
    """
    from authzed.api.v1 import schema_service_pb2 as ssp

    c = make_client(max_retries=2)
    ok = ssp.ReadSchemaResponse(schema_text="definition user {}")
    stub = mock.Mock(side_effect=[_SyncRpcError(grpc.StatusCode.UNAVAILABLE), ok])
    with mock.patch.object(c._schema, "ReadSchema", stub):
        c.read_schema()
    assert stub.call_count == 2, "a transient error must be retried"


def test_mutation_transient_error_is_attempted_once_only(make_client):
    """A mutation (WriteRelationships) is NOT retried, even on a retryable
    code: it may carry OPERATION_CREATE or preconditions, and if it
    actually committed but the response was lost, a retry would surface
    ALREADY_EXISTS/FAILED_PRECONDITION for a write that in fact succeeded.
    DESIGN.md, "Automatic retry is for idempotent operations only"."""
    c = make_client(max_retries=3)
    stub = mock.Mock(side_effect=_SyncRpcError(grpc.StatusCode.UNAVAILABLE))
    with mock.patch.object(c._permissions, "WriteRelationships", stub):
        from spicedb import Transaction

        txn = Transaction()
        txn.create(Relationship.from_triple("document:a", "viewer", "user:jimmy"))
        with pytest.raises(UnavailableError):
            c.write(txn)
    assert stub.call_count == 1, "mutations must be attempted exactly once"


def test_resource_exhausted_is_never_retried(make_client):
    from spicedb.errors import ResourceExhaustedError

    c = make_client(max_retries=3)
    stub = mock.Mock(side_effect=_SyncRpcError(grpc.StatusCode.RESOURCE_EXHAUSTED))
    with mock.patch.object(c._schema, "ReadSchema", stub):
        with pytest.raises(ResourceExhaustedError):
            c.read_schema()
    assert stub.call_count == 1, "RESOURCE_EXHAUSTED must never be retried"


def test_backoff_has_jitter(make_client):
    """Backoff must vary between runs -- assert the jitter exists, not an
    exact value. Without jitter every client in a fleet retries on the
    same schedule after a server restart (thundering herd)."""
    c = make_client(max_retries=1)
    seen = {c._backoff_seconds(2) for _ in range(50)}
    assert len(seen) > 1, "backoff should vary between calls"
    assert all(0 <= v <= 0.4 for v in seen)


def test_transient_error_gives_up_after_max_retries(make_client):
    c = make_client(max_retries=1)
    stub = mock.Mock(side_effect=_SyncRpcError(grpc.StatusCode.UNAVAILABLE))
    with mock.patch.object(c._schema, "ReadSchema", stub):
        with pytest.raises(UnavailableError):
            c.read_schema()
    assert stub.call_count == 2, "initial attempt + 1 retry"


def test_non_transient_error_is_not_retried(make_client):
    c = make_client(max_retries=3)
    stub = mock.Mock(side_effect=_SyncRpcError(grpc.StatusCode.PERMISSION_DENIED))
    with mock.patch.object(c._schema, "ReadSchema", stub):
        with pytest.raises(PermissionDeniedError):
            c.read_schema()
    assert stub.call_count == 1


def test_no_event_loop_is_required_anywhere():
    import asyncio

    with pytest.raises(RuntimeError):
        asyncio.get_running_loop()  # proves we are outside a loop
    assert SpiceDBClient("localhost:50051", token="t", insecure=True)._channel is None


def test_read_relationships_returns_a_plain_iterator(make_client):
    from authzed.api.v1 import permission_service_pb2 as psp

    c = make_client()
    page = [
        psp.ReadRelationshipsResponse(
            relationship=Relationship.from_triple(
                f"document:{i}", "viewer", "user:jimmy"
            )._to_proto()
        )
        for i in range(3)
    ]
    with mock.patch.object(c._permissions, "ReadRelationships", return_value=iter(page)):
        got = list(c.read_relationships(Filter(resource_type="document"), full()))
    assert [r.resource_id for r in got] == ["0", "1", "2"]


def test_read_relationships_stops_when_page_is_short(make_client):
    """A page smaller than DEFAULT_PAGE_SIZE means the last page -- do not re-request."""
    from authzed.api.v1 import permission_service_pb2 as psp

    c = make_client()
    stub = mock.Mock(
        return_value=iter(
            [
                psp.ReadRelationshipsResponse(
                    relationship=Relationship.from_triple(
                        "document:a", "viewer", "user:jimmy"
                    )._to_proto()
                )
            ]
        )
    )
    with mock.patch.object(c._permissions, "ReadRelationships", stub):
        list(c.read_relationships(Filter(resource_type="document"), full()))
    assert stub.call_count == 1


def test_streaming_retries_establishment_only_before_first_yield(make_client):
    """Retrying after an item was yielded would duplicate it for the caller."""
    from authzed.api.v1 import permission_service_pb2 as psp

    c = make_client(max_retries=3)

    def _one_then_fail():
        yield psp.ReadRelationshipsResponse(
            relationship=Relationship.from_triple(
                "document:a", "viewer", "user:jimmy"
            )._to_proto()
        )
        raise _SyncRpcError(grpc.StatusCode.UNAVAILABLE)

    stub = mock.Mock(return_value=_one_then_fail())
    with mock.patch.object(c._permissions, "ReadRelationships", stub):
        it = c.read_relationships(Filter(resource_type="document"), full())
        assert next(it).resource_id == "a"
        with pytest.raises(UnavailableError):
            next(it)
    assert stub.call_count == 1, "must NOT retry after an item was yielded"


def test_import_relationships_returns_num_loaded(make_client):
    from authzed.api.v1 import permission_service_pb2 as psp

    c = make_client()
    with mock.patch.object(
        c._permissions,
        "ImportBulkRelationships",
        return_value=psp.ImportBulkRelationshipsResponse(num_loaded=2),
    ):
        rels = [
            Relationship.from_triple("document:a", "viewer", "user:jimmy"),
            Relationship.from_triple("document:b", "viewer", "user:jimmy"),
        ]
        assert c.import_relationships(rels) == 2
