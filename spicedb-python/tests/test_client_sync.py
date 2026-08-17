"""Sync client: wiring, retry, and lifecycle.

Request-building and response-mapping are covered once in test_requests.py and
test_mapping.py -- this file only asserts the sync client drives them correctly.
"""

from unittest import mock

import grpc
import pytest

from spicedb import Filter, Relationship
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


def _client(**kw) -> SpiceDBClient:
    return SpiceDBClient("localhost:50051", token="t", insecure=True, **kw)


def test_construction_opens_no_channel():
    assert _client()._channel is None


def test_close_before_any_call_is_a_noop():
    _client().close()  # must not raise


def test_context_manager_closes_the_channel():
    c = _client()
    with c as entered:
        assert entered is c
        entered._ensure_channel()
        assert entered._channel is not None
    assert c._channel is None


def test_check_permission_returns_bool():
    from authzed.api.v1 import permission_service_pb2 as psp

    c = _client()
    c._ensure_channel()
    resp = psp.CheckBulkPermissionsResponse(
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
        assert c.check_permission(full(), rel) is True


def test_transient_error_is_retried_then_succeeds():
    """Guards the Task 1 is_transient fix -- without it this retries zero times."""
    from authzed.api.v1 import permission_service_pb2 as psp

    c = _client(max_retries=2)
    c._ensure_channel()
    ok = psp.WriteRelationshipsResponse()
    stub = mock.Mock(side_effect=[_SyncRpcError(grpc.StatusCode.UNAVAILABLE), ok])
    with mock.patch.object(c._permissions, "WriteRelationships", stub):
        from spicedb import Transaction

        txn = Transaction()
        txn.create(Relationship.from_triple("document:a", "viewer", "user:jimmy"))
        c.write(txn)
    assert stub.call_count == 2, "a transient error must be retried"


def test_transient_error_gives_up_after_max_retries():
    c = _client(max_retries=1)
    c._ensure_channel()
    stub = mock.Mock(side_effect=_SyncRpcError(grpc.StatusCode.UNAVAILABLE))
    with mock.patch.object(c._schema, "ReadSchema", stub):
        with pytest.raises(UnavailableError):
            c.read_schema()
    assert stub.call_count == 2, "initial attempt + 1 retry"


def test_non_transient_error_is_not_retried():
    c = _client(max_retries=3)
    c._ensure_channel()
    stub = mock.Mock(side_effect=_SyncRpcError(grpc.StatusCode.PERMISSION_DENIED))
    with mock.patch.object(c._schema, "ReadSchema", stub):
        with pytest.raises(PermissionDeniedError):
            c.read_schema()
    assert stub.call_count == 1


def test_no_event_loop_is_required_anywhere():
    import asyncio

    with pytest.raises(RuntimeError):
        asyncio.get_running_loop()  # proves we are outside a loop
    assert _client()._channel is None


def test_read_relationships_returns_a_plain_iterator():
    from authzed.api.v1 import permission_service_pb2 as psp

    c = _client()
    c._ensure_channel()
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


def test_read_relationships_stops_when_page_is_short():
    """A page smaller than DEFAULT_PAGE_SIZE means the last page -- do not re-request."""
    from authzed.api.v1 import permission_service_pb2 as psp

    c = _client()
    c._ensure_channel()
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


def test_streaming_retries_establishment_only_before_first_yield():
    """Retrying after an item was yielded would duplicate it for the caller."""
    from authzed.api.v1 import permission_service_pb2 as psp

    c = _client(max_retries=3)
    c._ensure_channel()

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


def test_import_relationships_returns_num_loaded():
    from authzed.api.v1 import permission_service_pb2 as psp

    c = _client()
    c._ensure_channel()
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
