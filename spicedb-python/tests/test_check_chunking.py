"""Bulk-check chunking, for both `spicedb.sync` and `spicedb.aio`.

SpiceDB rejects a `CheckBulkPermissions` request carrying more items than
`maxBulkCheckCount` -- 10,000, a hard-coded const in
`internal/services/v1/bulkcheck.go` with no flag to raise or lower it --
with `ERROR_REASON_TOO_MANY_CHECKS_IN_REQUEST`. Nothing in the proto
enforces this (`CheckBulkPermissionsRequest.items` carries only a per-item
`required` rule, not a collection-size rule), so the client is what has to
split large inputs.
"""

from unittest import mock
from unittest.mock import AsyncMock

import pytest
from authzed.api.v1 import core_pb2, permission_service_pb2

import spicedb.aio
import spicedb.sync
from spicedb import Relationship
from spicedb._requests import CHECK_BATCH_SIZE
from spicedb.consistency import full
from spicedb.errors import SpiceDBError


def numbered_rels(n):
    """`n` relationships whose resource IDs are their index, zero-padded so
    lexical and numeric order agree when read by eye."""
    return [
        Relationship.from_triple(f"document:{i:05d}", "view", "user:alice")
        for i in range(n)
    ]


def echo_response(request, short_by=0, malformed_local=None):
    """A response answering `request`, echoing each item's resource ID back
    through `missing_required_context` so a caller can prove which request
    item every result came from -- and therefore that concatenating chunk
    responses preserved input order.

    `short_by` drops that many pairs off the end, exercising the pair-count
    guard.
    """
    items = request.items[: len(request.items) - short_by] if short_by else request.items

    def pair(i, item):
        if malformed_local == i:
            # `response` oneof left unset entirely.
            return permission_service_pb2.CheckBulkPermissionsPair()
        return permission_service_pb2.CheckBulkPermissionsPair(
            item=permission_service_pb2.CheckBulkPermissionsResponseItem(
                permissionship=permission_service_pb2.CheckPermissionResponse.PERMISSIONSHIP_HAS_PERMISSION,
                partial_caveat_info=core_pb2.PartialCaveatInfo(
                    missing_required_context=[item.resource.object_id]
                ),
            )
        )

    return permission_service_pb2.CheckBulkPermissionsResponse(
        checked_at=core_pb2.ZedToken(token="tok"),
        pairs=[pair(i, item) for i, item in enumerate(items)],
    )


class _Recorder:
    """Stands in for the `CheckBulkPermissions` stub, recording the item
    count of every request it is handed."""

    def __init__(self, short_at=None, malformed_at_absolute=None):
        self.sizes = []
        self._short_at = short_at
        self._malformed_at_absolute = malformed_at_absolute

    def _respond(self, request):
        index = len(self.sizes)
        base = sum(self.sizes)
        self.sizes.append(len(request.items))
        malformed_local = None
        if self._malformed_at_absolute is not None:
            local = self._malformed_at_absolute - base
            if 0 <= local < len(request.items):
                malformed_local = local
        return echo_response(
            request,
            short_by=1 if self._short_at == index else 0,
            malformed_local=malformed_local,
        )

    def sync_stub(self):
        return mock.Mock(side_effect=lambda request, **kw: self._respond(request))

    def aio_stub(self):
        return AsyncMock(side_effect=lambda request, **kw: self._respond(request))


@pytest.fixture
def make_sync_client():
    created = []

    def _make(**kw):
        c = spicedb.sync.SpiceDBClient(
            "localhost:50051", token="t", insecure=True, **kw
        )
        c._ensure_channel()
        created.append(c)
        return c

    yield _make
    for c in created:
        c.close()


@pytest.fixture
async def make_aio_client():
    created = []

    def _make(**kw):
        c = spicedb.aio.SpiceDBClient(
            "localhost:50051", token="t", insecure=True, **kw
        )
        c._ensure_channel()
        created.append(c)
        return c

    yield _make
    for c in created:
        await c.close()


TOTAL = CHECK_BATCH_SIZE * 2 + 7
EXPECTED_SIZES = [CHECK_BATCH_SIZE, CHECK_BATCH_SIZE, 7]


class TestSyncCheckChunking:
    def test_oversized_input_is_split_into_chunks(self, make_sync_client):
        """The client must not forward an unbounded caller list as one
        request -- the server rejects it outright past 10,000 items."""
        client = make_sync_client()
        recorder = _Recorder()
        client._permissions.CheckBulkPermissions = recorder.sync_stub()

        results = client.check_permissions(full(), *numbered_rels(TOTAL))

        assert len(results) == TOTAL
        assert recorder.sizes == EXPECTED_SIZES

    def test_chunked_results_stay_in_input_order(self, make_sync_client):
        """Concatenating chunk responses must preserve the caller's order
        across chunk boundaries. The echo carries each item's own ID, so a
        reordering is visible on every one of the 2,007 results."""
        client = make_sync_client()
        recorder = _Recorder()
        client._permissions.CheckBulkPermissions = recorder.sync_stub()

        results = client.check_permissions(full(), *numbered_rels(TOTAL))

        assert [r.missing_context[0] for r in results] == [
            f"{i:05d}" for i in range(TOTAL)
        ]

    @pytest.mark.parametrize("n", [1, 999, CHECK_BATCH_SIZE])
    def test_under_chunk_size_sends_exactly_one_request(self, make_sync_client, n):
        """The common case must not regress into a loop with per-chunk
        overhead."""
        client = make_sync_client()
        recorder = _Recorder()
        client._permissions.CheckBulkPermissions = recorder.sync_stub()

        results = client.check_permissions(full(), *numbered_rels(n))

        assert len(results) == n
        assert recorder.sizes == [n]

    def test_empty_input_sends_no_request(self, make_sync_client):
        """Zero relationships costs zero round trips -- not one request
        carrying an empty item list -- and returns [] rather than raising."""
        client = make_sync_client()
        recorder = _Recorder()
        client._permissions.CheckBulkPermissions = recorder.sync_stub()

        assert client.check_permissions(full()) == []
        assert recorder.sizes == []

    def test_check_all_on_empty_input_is_false_and_sends_no_request(
        self, make_sync_client
    ):
        """Chunking must not resurrect the vacuous-true bug: an aggregate
        over zero checks is False, and it costs no request."""
        client = make_sync_client()
        recorder = _Recorder()
        client._permissions.CheckBulkPermissions = recorder.sync_stub()

        assert client.check_all(full()) is False
        assert recorder.sizes == []

    def test_length_guard_fires_on_a_later_chunk(self, make_sync_client):
        """The pair-count guard is evaluated per chunk, not once against the
        caller's total: the second chunk answers 999 pairs for 1,000 items."""
        client = make_sync_client()
        recorder = _Recorder(short_at=1)
        client._permissions.CheckBulkPermissions = recorder.sync_stub()

        with pytest.raises(SpiceDBError) as excinfo:
            client.check_permissions(full(), *numbered_rels(TOTAL))

        assert "999 pair(s)" in str(excinfo.value)
        assert f"{CHECK_BATCH_SIZE} request item(s)" in str(excinfo.value)
        # Two requests went out before the guard fired -- proof the failure
        # was detected on the second chunk, not on the whole input up front.
        assert recorder.sizes == [CHECK_BATCH_SIZE, CHECK_BATCH_SIZE]

    def test_malformed_pair_reports_the_callers_absolute_index(self, make_sync_client):
        """The index in a per-item message must be the caller's own, not the
        index within whichever chunk happened to carry the failure.

        Chunking made every "check item N" message chunk-relative: a failure
        at relationship 1003 read as "check item 3", so a caller who logs or
        parses it acts on relationship 3 -- one resource's answer attributed
        to another, the same failure family the pair-count guard exists to
        prevent, relocated into the diagnostic.
        """
        failing = CHECK_BATCH_SIZE + 3
        client = make_sync_client()
        recorder = _Recorder(malformed_at_absolute=failing)
        client._permissions.CheckBulkPermissions = recorder.sync_stub()

        with pytest.raises(SpiceDBError) as excinfo:
            client.check_permissions(full(), *numbered_rels(CHECK_BATCH_SIZE * 2))

        assert f"check item {failing}:" in str(excinfo.value)
        assert "check item 3:" not in str(excinfo.value)


class TestAioCheckChunking:
    async def test_oversized_input_is_split_into_chunks(self, make_aio_client):
        client = make_aio_client()
        recorder = _Recorder()
        client._permissions.CheckBulkPermissions = recorder.aio_stub()

        results = await client.check_permissions(full(), *numbered_rels(TOTAL))

        assert len(results) == TOTAL
        assert recorder.sizes == EXPECTED_SIZES

    async def test_chunked_results_stay_in_input_order(self, make_aio_client):
        client = make_aio_client()
        recorder = _Recorder()
        client._permissions.CheckBulkPermissions = recorder.aio_stub()

        results = await client.check_permissions(full(), *numbered_rels(TOTAL))

        assert [r.missing_context[0] for r in results] == [
            f"{i:05d}" for i in range(TOTAL)
        ]

    @pytest.mark.parametrize("n", [1, 999, CHECK_BATCH_SIZE])
    async def test_under_chunk_size_sends_exactly_one_request(self, make_aio_client, n):
        client = make_aio_client()
        recorder = _Recorder()
        client._permissions.CheckBulkPermissions = recorder.aio_stub()

        results = await client.check_permissions(full(), *numbered_rels(n))

        assert len(results) == n
        assert recorder.sizes == [n]

    async def test_empty_input_sends_no_request(self, make_aio_client):
        client = make_aio_client()
        recorder = _Recorder()
        client._permissions.CheckBulkPermissions = recorder.aio_stub()

        assert await client.check_permissions(full()) == []
        assert recorder.sizes == []

    async def test_check_all_on_empty_input_is_false_and_sends_no_request(
        self, make_aio_client
    ):
        client = make_aio_client()
        recorder = _Recorder()
        client._permissions.CheckBulkPermissions = recorder.aio_stub()

        assert await client.check_all(full()) is False
        assert recorder.sizes == []

    async def test_length_guard_fires_on_a_later_chunk(self, make_aio_client):
        client = make_aio_client()
        recorder = _Recorder(short_at=1)
        client._permissions.CheckBulkPermissions = recorder.aio_stub()

        with pytest.raises(SpiceDBError) as excinfo:
            await client.check_permissions(full(), *numbered_rels(TOTAL))

        assert "999 pair(s)" in str(excinfo.value)
        assert f"{CHECK_BATCH_SIZE} request item(s)" in str(excinfo.value)
        assert recorder.sizes == [CHECK_BATCH_SIZE, CHECK_BATCH_SIZE]

    async def test_malformed_pair_reports_the_callers_absolute_index(self, make_aio_client):
        failing = CHECK_BATCH_SIZE + 3
        client = make_aio_client()
        recorder = _Recorder(malformed_at_absolute=failing)
        client._permissions.CheckBulkPermissions = recorder.aio_stub()

        with pytest.raises(SpiceDBError) as excinfo:
            await client.check_permissions(full(), *numbered_rels(CHECK_BATCH_SIZE * 2))

        assert f"check item {failing}:" in str(excinfo.value)
        assert "check item 3:" not in str(excinfo.value)
