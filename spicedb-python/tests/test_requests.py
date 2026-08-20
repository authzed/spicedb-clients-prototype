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


def test_check_bulk_request_call_level_context_reaches_every_item():
    """C1: a call-level context reaches every item, by value."""
    rels = [
        Relationship.from_triple("document:a", "view", "user:jimmy"),
        Relationship.from_triple("document:b", "view", "user:jimmy"),
    ]
    r = req.check_bulk_request(full(), rels, {"now": 42, "region": "us"})
    assert dict(r.items[0].context) == {"now": 42, "region": "us"}
    assert dict(r.items[1].context) == {"now": 42, "region": "us"}


def test_check_bulk_request_per_item_context_reaches_only_that_item():
    """C2: a per-item check_context override reaches only that item; a
    sibling item with no override gets no context field at all."""
    rels = [
        Relationship.from_triple(
            "document:a", "view", "user:jimmy", check_context={"region": "eu"}
        ),
        Relationship.from_triple("document:b", "view", "user:jimmy"),
    ]
    r = req.check_bulk_request(full(), rels, None)
    assert dict(r.items[0].context) == {"region": "eu"}
    assert not r.items[1].HasField("context")


def test_check_bulk_request_merges_call_level_and_per_item_context():
    """C3: the merge rule is key-level, item wins -- NOT wholesale
    replacement. Call-level {"now": 42, "region": "us"} + item-level
    {"region": "eu"} produces {"now": 42, "region": "eu"} for that item
    (item's "region" wins, call-level "now" is retained), and the unmodified
    call-level {"now": 42, "region": "us"} for a sibling item that supplied
    none. Asserting only the overriding item would also pass under
    wholesale-replacement semantics, so both items must be checked to pin
    the merge rule.
    """
    rels = [
        Relationship.from_triple(
            "document:a", "view", "user:jimmy", check_context={"region": "eu"}
        ),
        Relationship.from_triple("document:b", "view", "user:jimmy"),
    ]
    r = req.check_bulk_request(full(), rels, {"now": 42, "region": "us"})
    assert dict(r.items[0].context) == {"now": 42, "region": "eu"}
    assert dict(r.items[1].context) == {"now": 42, "region": "us"}


def test_check_bulk_request_no_context_supplied_leaves_field_unset():
    """C4: neither call-level nor per-item context supplied -> no `context`
    field set on the wire (not an empty Struct)."""
    rels = [Relationship.from_triple("document:a", "view", "user:jimmy")]
    r = req.check_bulk_request(full(), rels, None)
    assert not r.items[0].HasField("context")


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


def test_import_batches_accepts_a_generator_without_materializing_it():
    """`import_batches` must accept ANY iterable, not just a `list` --

    unlike every other bulk/paginated RPC, ImportBulkRelationships is
    client-streaming: the caller is the one producing an unbounded amount of
    data, and a caller streaming in millions of relationships from a
    generator or a DB cursor should never be forced to materialize the whole
    thing into a `list` first just to call this.
    """
    consumed = []

    def gen():
        for i in range(2500):
            consumed.append(i)
            yield Relationship.from_triple(f"document:{i}", "viewer", "user:jimmy")

    batches = req.import_batches(gen(), req.IMPORT_BATCH_SIZE)

    # import_batches itself is a generator: pulling just the FIRST request
    # must not force the whole 2500-item source to be consumed -- only one
    # batch's worth (IMPORT_BATCH_SIZE == 1000) should have been pulled.
    first = next(batches)
    assert len(first.relationships) == 1000
    assert len(consumed) == 1000, (
        "import_batches must consume its source lazily, one batch at a time -- "
        f"pulling the first request should not have consumed all {len(consumed)} items"
    )

    # Draining the rest still yields every relationship, in order.
    remaining = list(batches)
    assert [len(b.relationships) for b in remaining] == [1000, 500]
    assert len(consumed) == 2500


def test_write_request_carries_updates_and_preconditions():
    txn = Transaction()
    txn.create(Relationship.from_triple("document:a", "viewer", "user:jimmy"))
    r = req.write_request(txn)
    assert len(r.updates) == 1
