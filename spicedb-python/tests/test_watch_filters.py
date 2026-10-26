"""Watch snapshots full OR filters and rejects conflicting scopes before RPC."""
from unittest.mock import Mock

import pytest
from authzed.api.v1 import permission_service_pb2 as p
from spicedb import Filter, SubjectFilter, SubjectRelationFilter, InvalidArgumentError
from spicedb.sync import SpiceDBClient
from spicedb.aio import SpiceDBClient as AioClient


class EmptyCall:
    def __iter__(self):
        return iter(())

    async def __aiter__(self):
        for item in ():
            yield item

    def cancel(self):
        pass


@pytest.mark.parametrize("client_type", [SpiceDBClient, AioClient])
@pytest.mark.parametrize("scope", ["filters", "empty", "object_types"])
async def test_watch_filter_wire_and_snapshot(client_type, scope):
    client = client_type("localhost:50051", token="key")
    client._ensure_channel = Mock()
    rpc = Mock(return_value=EmptyCall())
    client._watch = Mock(Watch=rpc)
    filters = [
        Filter("document", resource_id="one", relation="viewer", subject=SubjectFilter(
            "user", subject_id="alice", relation=SubjectRelationFilter.direct())),
        Filter("folder", resource_id_prefix="team/", relation="editor", subject=SubjectFilter(
            "group", subject_id="eng", relation=SubjectRelationFilter.named("member"))),
    ] if scope == "filters" else []
    object_types = ["document"] if scope == "object_types" else []
    stream = client.watch(filters=filters, object_types=object_types)
    filters.clear()
    object_types.clear()
    if client_type is AioClient:
        assert [event async for event in stream] == []
    else:
        assert list(stream) == []
    request = rpc.call_args.args[0]
    expected = [
        p.RelationshipFilter(resource_type="document", optional_resource_id="one", optional_relation="viewer",
            optional_subject_filter=p.SubjectFilter(subject_type="user", optional_subject_id="alice",
                optional_relation=p.SubjectFilter.RelationFilter(relation=""))),
        p.RelationshipFilter(resource_type="folder", optional_resource_id_prefix="team/", optional_relation="editor",
            optional_subject_filter=p.SubjectFilter(subject_type="group", optional_subject_id="eng",
                optional_relation=p.SubjectFilter.RelationFilter(relation="member"))),
    ] if scope == "filters" else []
    assert list(request.optional_relationship_filters) == expected
    assert list(request.optional_object_types) == (["document"] if scope == "object_types" else [])


@pytest.mark.parametrize("client_type", [SpiceDBClient, AioClient])
def test_watch_conflicting_scopes_rejected_before_transport(client_type):
    client = client_type("localhost:50051", token="key")
    client._ensure_channel = Mock(side_effect=AssertionError("transport reached"))
    with pytest.raises(InvalidArgumentError, match="mutually exclusive"):
        client.watch(object_types=["document"], filters=[Filter("document")])
    client._ensure_channel.assert_not_called()
