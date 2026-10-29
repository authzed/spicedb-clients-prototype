"""Responses the client cannot interpret fail with INTERNAL.

Root DESIGN.md, "Expose native errors without losing server diagnostics": a
missing required field or a cursor that does not advance fails with
INTERNAL, which no retry policy retries by default. Such a response is never
skipped (a skipped item makes a full page look short and ends a paged read
early) and never filled with empty values. Client-detected errors never use
UNKNOWN.
"""

from __future__ import annotations

from unittest.mock import AsyncMock, Mock

import grpc
import pytest
from authzed.api.materialize.v0 import watchpermissionsets_pb2 as m
from authzed.api.v1 import core_pb2 as core
from authzed.api.v1 import experimental_service_pb2 as e
from authzed.api.v1 import permission_service_pb2 as p
from authzed.api.v1 import watch_service_pb2 as w

from spicedb import (

    Filter,
    InternalError,
    ObjectRef,
    PermissionCheck,
    Relationship,
    SubjectRef,
    TransportSecurity,
    Tracing,
    full,
)
from spicedb import _mapping
from spicedb.options import DEFAULT_RETRY_POLICY
from spicedb.aio import SpiceDBClient as AioClient
from spicedb.sync import SpiceDBClient as SyncClient

FLAVORS = [SyncClient, AioClient]
T = core.ZedToken(token="t")
_ALICE = SubjectRef(ObjectRef("user", "alice"))


class _Call:
    def __init__(self, responses):
        self.responses = responses

    def __iter__(self):
        return iter(self.responses)

    async def __aiter__(self):
        for r in self.responses:
            yield r

    def cancel(self):
        return True


def _client(client_type):
    client = client_type("localhost:1", token="t", transport_security=TransportSecurity.PLAINTEXT)
    client._ensure_channel = Mock()
    return client


def _rel(**overrides):
    rel = Relationship.from_tuple("document:a#viewer@user:b")._to_proto()
    for name, value in overrides.items():
        if value is None:
            rel.ClearField(name)
        elif isinstance(value, str):
            setattr(rel, name, value)
        else:
            getattr(rel, name).CopyFrom(value)
    return rel


def _assert_internal(caught, field):
    assert type(caught.value) is InternalError
    assert caught.value.code() is grpc.StatusCode.INTERNAL
    assert field in str(caught.value)


BAD_RELATIONSHIPS = {
    "missing relationship": (None, "relationship"),
    "missing resource": (_rel(resource=None), "resource"),
    "empty resource id": (
        _rel(resource=core.ObjectReference(object_type="document", object_id="")),
        "resource.object_id",
    ),
    "empty relation": (_rel(relation=""), "relation"),
    "missing subject": (_rel(subject=None), "subject"),
    "missing subject object": (_rel(subject=core.SubjectReference()), "subject"),
}


@pytest.mark.parametrize("client_type", FLAVORS)
@pytest.mark.parametrize("case", sorted(BAD_RELATIONSHIPS))
async def test_read_relationships_fails_instead_of_skipping(client_type, case):
    bad, field = BAD_RELATIONSHIPS[case]
    good = p.ReadRelationshipsResponse(read_at=T, relationship=_rel(), after_result_cursor=core.Cursor(token="1"))
    broken = p.ReadRelationshipsResponse(read_at=T, after_result_cursor=core.Cursor(token="2"))
    if bad is not None:
        broken.relationship.CopyFrom(bad)
    client = _client(client_type)
    client._permissions = Mock(ReadRelationships=lambda request, **kw: _Call([good, broken]))
    seen = []
    with pytest.raises(InternalError) as caught:
        async for item in _aiter(client.read_relationships(Filter("document"), consistency=full(), page_size=2)):
            seen.append(item)
    _assert_internal(caught, field)
    assert len(seen) == 1  # the good item, then a failure: never a short page


@pytest.mark.parametrize("client_type", FLAVORS)
async def test_read_relationships_without_revision_fails(client_type):
    client = _client(client_type)
    client._permissions = Mock(
        ReadRelationships=lambda request, **kw: _Call([p.ReadRelationshipsResponse(relationship=_rel())])
    )
    with pytest.raises(InternalError) as caught:
        await _collect(client.read_relationships(Filter("document"), consistency=full()))
    _assert_internal(caught, "read_at")


@pytest.mark.parametrize("client_type", FLAVORS)
async def test_export_fails_on_a_relationship_without_resource(client_type):
    client = _client(client_type)
    client._permissions = Mock(ExportBulkRelationships=lambda request, **kw: _Call([
        p.ExportBulkRelationshipsResponse(relationships=[_rel(), _rel(resource=None)])
    ]))
    with pytest.raises(InternalError) as caught:
        await _collect(client.export_relationships(consistency=full()))
    _assert_internal(caught, "resource")


@pytest.mark.parametrize("client_type", FLAVORS)
async def test_lookup_resources_fails_on_empty_id(client_type):
    client = _client(client_type)
    client._permissions = Mock(LookupResources=lambda request, **kw: _Call([
        p.LookupResourcesResponse(looked_up_at=T, resource_object_id="",
                                  permissionship=p.LOOKUP_PERMISSIONSHIP_HAS_PERMISSION)
    ]))
    with pytest.raises(InternalError) as caught:
        await _collect(client.lookup_resources(
            resource_type="document", permission="view", subject=_ALICE, consistency=full()
        ))
    _assert_internal(caught, "resource_object_id")


@pytest.mark.parametrize("client_type", FLAVORS)
async def test_lookup_subjects_fails_on_missing_subject(client_type):
    client = _client(client_type)
    client._permissions = Mock(LookupSubjects=lambda request, **kw: _Call([
        p.LookupSubjectsResponse(looked_up_at=T)
    ]))
    with pytest.raises(InternalError) as caught:
        await _collect(client.lookup_subjects(
            resource=ObjectRef("document", "a"), permission="view", subject_type="user", consistency=full()
        ))
    _assert_internal(caught, "subject_object_id")


def test_lookup_subject_fails_on_an_excluded_subject_without_id():
    response = p.LookupSubjectsResponse(
        looked_up_at=T,
        subject=p.ResolvedSubject(subject_object_id="*", permissionship=p.LOOKUP_PERMISSIONSHIP_HAS_PERMISSION),
        excluded_subjects=[p.ResolvedSubject(subject_object_id="")],
    )
    with pytest.raises(InternalError, match="excluded_subjects"):
        _mapping.lookup_subject(response)


@pytest.mark.parametrize("client_type", FLAVORS)
async def test_non_advancing_cursor_fails(client_type):
    """A full page that ends at its own start cursor would loop forever."""
    def read(request, **kw):
        return _Call([
            p.ReadRelationshipsResponse(read_at=T, relationship=_rel(), after_result_cursor=core.Cursor(token="same"))
        ])

    client = _client(client_type)
    client._permissions = Mock(ReadRelationships=read)
    with pytest.raises(InternalError) as caught:
        await _collect(client.read_relationships(Filter("document"), consistency=full(), page_size=1, cursor="same"))
    _assert_internal(caught, "does not advance")


def _leaf(object_id="a"):
    return core.PermissionRelationshipTree(
        expanded_object=core.ObjectReference(object_type="document", object_id=object_id),
        expanded_relation="view",
        leaf=core.DirectSubjectSet(subjects=[
            core.SubjectReference(object=core.ObjectReference(object_type="user", object_id="u"))
        ]),
    )


EXPAND_CASES = {
    "missing tree_root": (p.ExpandPermissionTreeResponse(expanded_at=T), "tree_root"),
    "node without leaf or intermediate": (
        p.ExpandPermissionTreeResponse(expanded_at=T, tree_root=core.PermissionRelationshipTree(
            expanded_object=core.ObjectReference(object_type="document", object_id="a"), expanded_relation="view",
        )),
        "neither intermediate nor leaf",
    ),
    "child without object id": (
        p.ExpandPermissionTreeResponse(expanded_at=T, tree_root=core.PermissionRelationshipTree(
            expanded_object=core.ObjectReference(object_type="document", object_id="a"), expanded_relation="view",
            intermediate=core.AlgebraicSubjectSet(operation=1, children=[_leaf(""), _leaf()]),
        )),
        "children[0].expanded_object.object_id",
    ),
    "leaf subject without object": (
        p.ExpandPermissionTreeResponse(expanded_at=T, tree_root=core.PermissionRelationshipTree(
            expanded_object=core.ObjectReference(object_type="document", object_id="a"), expanded_relation="view",
            leaf=core.DirectSubjectSet(subjects=[core.SubjectReference()]),
        )),
        "leaf.subjects[0]",
    ),
    "missing expanded_at": (p.ExpandPermissionTreeResponse(tree_root=_leaf()), "expanded_at"),
}


@pytest.mark.parametrize("client_type", FLAVORS)
@pytest.mark.parametrize("case", sorted(EXPAND_CASES))
async def test_expand_fails_on_a_malformed_tree(client_type, case):
    response, field = EXPAND_CASES[case]
    client = _client(client_type)
    client._rpc = AsyncMock(return_value=response) if client_type is AioClient else Mock(return_value=response)
    client._permissions = Mock()
    with pytest.raises(InternalError) as caught:
        await _resolve(client.expand_permission_tree(
            resource=ObjectRef("document", "a"), permission="view", consistency=full()
        ))
    _assert_internal(caught, field)


@pytest.mark.parametrize("client_type", FLAVORS)
async def test_counter_without_value_or_calculating_flag_fails(client_type):
    client = _client(client_type)
    response = e.ExperimentalCountRelationshipsResponse()
    client._rpc = AsyncMock(return_value=response) if client_type is AioClient else Mock(return_value=response)
    client._experimental = Mock()
    with pytest.raises(InternalError) as caught:
        await _resolve(client.experimental_count_relationships("c"))
    _assert_internal(caught, "neither read_counter_value nor counter_still_calculating")


def _change(**overrides):
    change = m.PermissionSetChange(
        at_revision=T,
        operation=m.PermissionSetChange.SET_OPERATION_ADDED,
        parent_set=m.SetReference(object_type="group", object_id="eng", permission_or_relation="member"),
        child_member=m.MemberReference(object_type="user", object_id="alice"),
    )
    for name in overrides:
        change.ClearField(name)
    return change


PERMISSION_SET_CASES = {
    "missing change": (m.LookupPermissionSetsResponse(cursor=m.Cursor(cursor="c")), "change"),
    "missing cursor": (m.LookupPermissionSetsResponse(change=_change()), "cursor"),
    "missing parent": (m.LookupPermissionSetsResponse(change=_change(parent_set=1), cursor=m.Cursor()), "parent_set"),
    "missing child": (
        m.LookupPermissionSetsResponse(change=_change(child_member=1), cursor=m.Cursor()), "neither child_set"
    ),
    "missing revision": (m.LookupPermissionSetsResponse(change=_change(at_revision=1), cursor=m.Cursor()), "at_revision"),
}


@pytest.mark.parametrize("client_type", FLAVORS)
@pytest.mark.parametrize("case", sorted(PERMISSION_SET_CASES))
async def test_permission_set_lookup_fails_on_missing_fields(client_type, case):
    response, field = PERMISSION_SET_CASES[case]
    client = _client(client_type)
    client._permission_sets = Mock(LookupPermissionSets=lambda request, **kw: _Call([response]))
    with pytest.raises(InternalError) as caught:
        await _collect(client.lookup_permission_sets(limit=10))
    _assert_internal(caught, field)


def test_watch_update_without_relationship_fails():
    response = w.WatchResponse(changes_through=T, updates=[core.RelationshipUpdate(operation=1)])
    with pytest.raises(InternalError, match="relationship"):
        _mapping.watch_event(response)


def test_watch_without_changes_through_fails():
    with pytest.raises(InternalError, match="changes_through"):
        _mapping.watch_event(w.WatchResponse())


def test_check_bulk_count_mismatch_is_internal_not_unknown():
    item = PermissionCheck(ObjectRef("document", "a"), "view", _ALICE)
    response = p.CheckBulkPermissionsResponse(checked_at=T)
    from spicedb import _requests

    request = _requests.check_bulk_request(full(), [item], None, Tracing.DISABLED)
    with pytest.raises(InternalError) as caught:
        _mapping.check_results(response, request.items, 0, False)
    assert caught.value.code() is grpc.StatusCode.INTERNAL


@pytest.mark.parametrize("client_type", FLAVORS)
async def test_unspecified_delete_progress_is_internal(client_type):
    client = _client(client_type)
    response = p.DeleteRelationshipsResponse(deleted_at=T, relationships_deleted_count=1)
    client._rpc = AsyncMock(return_value=response) if client_type is AioClient else Mock(return_value=response)
    client._permissions = Mock()
    with pytest.raises(InternalError) as caught:
        await _resolve(client.delete_all_relationships_non_atomic(Filter("document")))
    _assert_internal(caught, "deletion_progress")


def test_relation_reference_without_names_fails():
    from authzed.api.v1 import schema_service_pb2 as sch
    from spicedb.types import RelationReference

    with pytest.raises(InternalError, match="relation_name"):
        RelationReference._from_proto(sch.ReflectionRelationReference(definition_name="document"))


def test_internal_is_not_retried_by_default():
    assert grpc.StatusCode.INTERNAL not in DEFAULT_RETRY_POLICY.retryable_codes


# -- helpers --------------------------------------------------------------


async def _resolve(value):
    import inspect

    return await value if inspect.isawaitable(value) else value


async def _collect(stream):
    return [x async for x in _aiter(stream)]


async def _aiter(stream):
    if hasattr(stream, "__anext__"):
        async for item in stream:
            yield item
    else:
        for item in stream:
            yield item


def test_protoconv_reports_a_caller_proto_as_local_validation():
    """A response proto the caller hands to protoconv is caller input."""
    from spicedb import LocalValidationError, protoconv

    with pytest.raises(LocalValidationError, match="looked_up_at"):
        protoconv.lookup_resource_from_proto(p.LookupResourcesResponse(resource_object_id="a"))
