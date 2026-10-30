"""Caller-supplied names, IDs and revision tokens are checked locally.

Root DESIGN.md, "Reject unusable credentials and trust material at
construction": a dynamically typed client type-checks names and IDs before
use, so ``42`` or ``True`` where a string belongs is a local validation
error, never a server round trip with a coerced value. An empty or
whitespace-only revision token is a local validation error too.
"""

from __future__ import annotations

import inspect
from unittest.mock import Mock

import pytest

from spicedb import (
    Caveat,
    Filter,
    LocalValidationError,
    ObjectRef,
    PermissionCheck,
    Relationship,
    SchemaFilter,
    SubjectFilter,
    SubjectRef,
    SubjectRelationFilter,
    TransportSecurity,
    ZedToken,
    at_least,
    at_least_or_full,
    at_least_or_min_latency,
    full,
    min_latency,
    snapshot,
)
from spicedb.aio import SpiceDBClient as AioClient
from spicedb.sync import SpiceDBClient as SyncClient
from spicedb.testing import AsyncInMemoryClient, InMemoryClient

DOC = ObjectRef("document", "readme")
ALICE = SubjectRef(ObjectRef("user", "alice"))
NOT_STRINGS = [42, True, 1.5, b"bytes"]


def _client(client_type):
    client = client_type("localhost:1", token="t", transport_security=TransportSecurity.PLAINTEXT)

    def no_rpc(*args, **kwargs):
        raise AssertionError("a wrongly typed input must not reach the server")

    client._ensure_channel = Mock()
    client._rpc = no_rpc
    client._permissions = Mock()
    client._schema = Mock()
    client._experimental = Mock()
    client._materialize = Mock()
    return client


async def _run(value):
    """Resolve a sync value, awaitable, or (async) stream, draining streams."""
    if inspect.isawaitable(value):
        value = await value
    if hasattr(value, "__aiter__"):
        return [item async for item in value]
    if hasattr(value, "__iter__") and not isinstance(value, (list, str)):
        return list(value)
    return value


# --- value constructors -----------------------------------------------------


@pytest.mark.parametrize("bad", NOT_STRINGS)
@pytest.mark.parametrize(
    "build",
    [
        lambda bad: ObjectRef("document", bad),
        lambda bad: ObjectRef(bad, "readme"),
        lambda bad: SubjectRef(ObjectRef("group", "eng"), bad),
        lambda bad: SubjectRef(bad),
        lambda bad: PermissionCheck(DOC, bad, ALICE),
        lambda bad: PermissionCheck(bad, "view", ALICE),
        lambda bad: PermissionCheck(DOC, "view", bad),
        lambda bad: Relationship(DOC, bad, ALICE),
        lambda bad: Relationship(DOC, "viewer", ALICE, Caveat(bad)),
        lambda bad: Relationship.from_triple(bad, "viewer", "user:alice"),
        lambda bad: Relationship.from_triple("document:readme", bad, "user:alice"),
        lambda bad: Relationship.from_triple("document:readme", "viewer", bad),
        lambda bad: Caveat(bad),
        lambda bad: Filter(bad),
        lambda bad: Filter("document", resource_id=bad),
        lambda bad: Filter("document", resource_id_prefix=bad),
        lambda bad: Filter("document", relation=bad),
        lambda bad: SubjectFilter(bad),
        lambda bad: SubjectFilter("user", subject_id=bad),
        lambda bad: SubjectRelationFilter.named(bad),
        lambda bad: SchemaFilter(definition_name=bad),
    ],
    ids=[
        "object_id", "object_type", "subject_relation", "subject_object",
        "check_permission", "check_resource", "check_subject",
        "relationship_relation", "relationship_caveat_name",
        "triple_resource", "triple_relation", "triple_subject",
        "caveat_name", "filter_resource_type", "filter_resource_id",
        "filter_resource_id_prefix", "filter_relation", "subject_filter_type",
        "subject_filter_id", "subject_relation_filter_name", "schema_filter",
    ],
)
def test_wrongly_typed_names_and_ids_are_local_validation_errors(build, bad):
    with pytest.raises(LocalValidationError, match="expected"):
        build(bad)


def test_string_values_are_still_accepted():
    Relationship(DOC, "viewer", SubjectRef(ObjectRef("group", "eng"), "member"), Caveat("ip"))
    Filter("document", resource_id="readme", relation="viewer", subject=SubjectFilter("user", subject_id="alice"))
    SchemaFilter(definition_name="document")


# --- client methods -----------------------------------------------------------


@pytest.mark.parametrize("client_type", [SyncClient, AioClient, InMemoryClient, AsyncInMemoryClient])
@pytest.mark.parametrize("bad", [42, True])
@pytest.mark.parametrize(
    "call",
    [
        lambda c, bad: c.lookup_resources(resource_type=bad, permission="view", subject=ALICE, consistency=full()),
        lambda c, bad: c.lookup_resources(resource_type="document", permission=bad, subject=ALICE, consistency=full()),
        lambda c, bad: c.lookup_subjects(resource=DOC, permission=bad, subject_type="user", consistency=full()),
        lambda c, bad: c.lookup_subjects(resource=DOC, permission="view", subject_type=bad, consistency=full()),
        lambda c, bad: c.lookup_subjects(
            resource=DOC, permission="view", subject_type="user", subject_relation=bad, consistency=full()
        ),
        lambda c, bad: c.write_schema(bad),
    ],
    ids=[
        "lookup_resources_type", "lookup_resources_permission", "lookup_subjects_permission",
        "lookup_subjects_type", "lookup_subjects_relation", "write_schema",
    ],
)
async def test_wrongly_typed_lookup_and_schema_arguments_never_reach_the_server(client_type, call, bad):
    client = _client(client_type) if client_type in (SyncClient, AioClient) else client_type()
    with pytest.raises(LocalValidationError, match="expected"):
        await _run(call(client, bad))


@pytest.mark.parametrize("client_type", [SyncClient, AioClient])
@pytest.mark.parametrize(
    "call",
    [
        lambda c: c.check(PermissionCheck(DOC, 42, ALICE), consistency=full()),
        lambda c: c.check_bulk([PermissionCheck(DOC, "view", ALICE), PermissionCheck(DOC, True, ALICE)], consistency=full()),
        lambda c: c.expand_permission_tree(resource=DOC, permission=42, consistency=full()),
        lambda c: c.computable_permissions(definition_name=42, relation_name="viewer", consistency=full()),
        lambda c: c.computable_permissions(definition_name="document", relation_name=True, consistency=full()),
        lambda c: c.dependent_relations(definition_name="document", permission_name=42, consistency=full()),
        lambda c: c.diff_schema(42, consistency=full()),
        lambda c: c.experimental_register_relationship_counter(42, Filter("document")),
        lambda c: c.experimental_unregister_relationship_counter(True),
        lambda c: c.experimental_count_relationships(42),
        lambda c: c.experimental_roaring_lookup_resources(
            resource_type=42, permission="view", subject=ALICE, consistency=full()
        ),
        lambda c: c.watch([42]),
        lambda c: c.read_relationships(Filter("document", resource_id=42), consistency=full()),
        lambda c: c.delete_relationships(Filter("document", relation=True)),
    ],
    ids=[
        "check", "check_bulk", "expand", "computable_definition", "computable_relation",
        "dependent", "diff_schema", "register_counter", "unregister_counter", "count",
        "roaring", "watch_object_types", "read_filter", "delete_filter",
    ],
)
async def test_wrongly_typed_names_never_reach_the_server(client_type, call):
    client = _client(client_type)
    with pytest.raises(LocalValidationError, match="expected"):
        await _run(call(client))


# --- revision tokens ----------------------------------------------------------


BLANK = ["", " ", "\t\n", "   "]


@pytest.mark.parametrize("blank", BLANK)
@pytest.mark.parametrize("helper", [at_least, snapshot])
def test_blank_revision_is_a_local_validation_error(helper, blank):
    with pytest.raises(LocalValidationError, match="nonempty ZedToken"):
        helper(ZedToken(blank))


@pytest.mark.parametrize("blank", BLANK)
def test_fallback_helpers_treat_a_blank_revision_as_absent(blank):
    assert at_least_or_full(ZedToken(blank)) == full()
    assert at_least_or_min_latency(ZedToken(blank)) == min_latency()


def test_padded_revision_is_kept_verbatim():
    """A token with real content is opaque: it is not trimmed."""
    assert str(at_least(ZedToken(" abc ")).token) == " abc "


@pytest.mark.parametrize("client_type", [SyncClient, AioClient])
@pytest.mark.parametrize("blank", ["", "  "])
@pytest.mark.parametrize(
    "call",
    [
        lambda c, t: c.watch(start_revision=t),
        lambda c, t: c.watch_permission_sets(start_revision=t),
        lambda c, t: c.lookup_permission_sets(at_revision=t),
        lambda c, t: c.download_permission_sets(at_revision=t),
    ],
    ids=["watch", "watch_permission_sets", "lookup_permission_sets", "download_permission_sets"],
)
async def test_blank_start_and_at_revisions_fail_before_any_rpc(client_type, call, blank):
    client = _client(client_type)
    with pytest.raises(LocalValidationError, match="nonempty ZedToken"):
        await _run(call(client, ZedToken(blank)))
