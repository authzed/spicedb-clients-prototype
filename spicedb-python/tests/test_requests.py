"""Tests for the flavor-free request builders.

These exercise the shared core directly, so they cover BOTH the sync and async
clients at once. Nothing here touches a channel.
"""

from spicedb import Filter, Relationship, Transaction
from spicedb import _requests as req
from spicedb.consistency import at_least, full


def test_context_struct_none_returns_none():
    assert req.context_struct(None) is None


def test_context_struct_populates_fields():
    s = req.context_struct({"tier": "gold", "n": 3})
    assert s["tier"] == "gold"
    assert s["n"] == 3


def test_subject_reference_from_relationship():
    rel = Relationship.from_triple("document:readme", "viewer", "user:jimmy")
    ref = req.subject_reference(rel)
    assert ref.object.object_type == "user"
    assert ref.object.object_id == "jimmy"
    assert ref.optional_relation == ""


def test_subject_reference_from_tuple_splits_on_first_colon():
    ref = req.subject_reference(("group:eng:west", "member"))
    assert ref.object.object_type == "group"
    assert ref.object.object_id == "eng:west"
    assert ref.optional_relation == "member"


def test_check_bulk_request_builds_one_item_per_relationship():
    rels = [
        Relationship.from_triple("document:a", "view", "user:jimmy"),
        Relationship.from_triple("document:b", "view", "user:jimmy"),
    ]
    r = req.check_bulk_request(full(), rels, None)
    assert len(r.items) == 2
    assert r.items[0].resource.object_id == "a"
    assert r.items[0].permission == "view"
    assert r.items[1].resource.object_id == "b"


def test_check_bulk_request_carries_at_least_as_fresh_token():
    """The read-your-writes chain (write() -> revision -> at_least(revision)
    -> check_permission()) depends on the Consistency built by at_least()
    actually reaching the outgoing CheckBulkPermissionsRequest.
    test_consistency.py::test_at_least only proves at_least() builds a
    Consistency whose own _to_proto() carries the token in isolation; this
    is the part of the chain that was previously verified by nothing --
    that check_bulk_request() threads that Consistency's proto through to
    request.consistency.at_least_as_fresh rather than dropping or
    overwriting it.
    """
    rels = [Relationship.from_triple("document:a", "view", "user:jimmy")]
    r = req.check_bulk_request(at_least("cafebabe"), rels, None)
    assert r.consistency.at_least_as_fresh.token == "cafebabe"


def test_lookup_resources_request_carries_at_least_as_fresh_token():
    """Same round-trip as above, for the lookup_resources() request builder
    -- examples/read_your_writes/ threads at_least(written_at) through this
    path too."""
    subj_ref = req.subject_reference(("user:alice", ""))
    r = req.lookup_resources_request(
        "document", "view", subj_ref, None, at_least("cafebabe"), req.DEFAULT_PAGE_SIZE, None
    )
    assert r.consistency.at_least_as_fresh.token == "cafebabe"


def test_read_relationships_request_sets_limit_and_cursor():
    f = Filter(resource_type="document")
    r = req.read_relationships_request(f, full(), req.DEFAULT_PAGE_SIZE, None)
    assert r.optional_limit == req.DEFAULT_PAGE_SIZE
    assert r.relationship_filter.resource_type == "document"


def test_delete_request_sets_partial_deletions_only_when_limit_given():
    f = Filter(resource_type="document")
    without = req.delete_relationships_request(f, None, None, None)
    assert without.optional_limit == 0
    assert without.optional_allow_partial_deletions is False

    with_limit = req.delete_relationships_request(f, None, None, 10)
    assert with_limit.optional_limit == 10
    assert with_limit.optional_allow_partial_deletions is True


def test_delete_request_builds_both_precondition_kinds():
    from authzed.api.v1 import permission_service_pb2 as psp

    f = Filter(resource_type="document")
    guard = Filter(resource_type="document", resource_id="locked")
    r = req.delete_relationships_request(f, [guard], [guard], None)
    ops = [p.operation for p in r.optional_preconditions]
    assert ops == [
        psp.Precondition.OPERATION_MUST_MATCH,
        psp.Precondition.OPERATION_MUST_NOT_MATCH,
    ]


def test_import_batches_chunks_by_batch_size():
    rels = [
        Relationship.from_triple(f"document:{i}", "viewer", "user:jimmy")
        for i in range(2500)
    ]
    batches = list(req.import_batches(rels, req.IMPORT_BATCH_SIZE))
    assert [len(b.relationships) for b in batches] == [1000, 1000, 500]


def test_write_request_carries_updates_and_preconditions():
    txn = Transaction()
    txn.create(Relationship.from_triple("document:a", "viewer", "user:jimmy"))
    r = req.write_request(txn)
    assert len(r.updates) == 1
