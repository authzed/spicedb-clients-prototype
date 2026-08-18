"""Example: Call deadlines.

Demonstrates the client-level `default_timeout` construction parameter, a
per-call `timeout` override on a unary call, and that bulk import
(`import_relationships`) is a client-streaming call that is NOT bounded by
`default_timeout` -- see root DESIGN.md, "RULE: A unary call must have a
deadline".
"""

import pytest

from spicedb import Filter, Relationship, full
from spicedb.aio import SpiceDBClient
from spicedb.types import Transaction


pytestmark = pytest.mark.integration


async def test_default_timeout_construction_param():
    # default_timeout applies to every unary call that doesn't pass its own
    # `timeout` override. This is the documented, real construction path --
    # not a mock -- so a signature drift here (e.g. the parameter silently
    # disappearing from the constructor) would fail this test, not just a
    # unit test against a stalling stub.
    async with SpiceDBClient(
        "localhost:50051",
        token="somerandomkeyhere",
        insecure=True,
        default_timeout=5.0,
    ) as client:
        await client.write_schema(
            "definition user {}\n\n"
            "definition document {\n"
            "    relation viewer: user\n"
            "    permission view = viewer\n"
            "}\n"
        )
        rel = Relationship.from_triple("document:readme", "viewer", "user:alice")
        txn = Transaction()
        txn.touch(rel)
        await client.write(txn)

        result = await client.check_permission(
            full(), Relationship.from_triple("document:readme", "view", "user:alice")
        )
        print(f"user:alice can view document:readme: {result.has_permission}")
        assert result.has_permission is True

        await client.delete_relationships(
            Filter(resource_type="document", resource_id="readme")
        )


async def test_per_call_timeout_overrides_default(client: SpiceDBClient):
    # A per-call timeout overrides whatever default_timeout the client was
    # constructed with (30s here, since `client` is the standard fixture).
    # 5 seconds is generous for a real call against a local SpiceDB -- this
    # is exercising the real timeout= parameter end-to-end, not testing how
    # small a timeout can be.
    txn = Transaction()
    txn.touch(Relationship.from_triple("document:readme", "viewer", "user:alice"))
    await client.write(txn, timeout=5.0)

    result = await client.check_permission(
        full(),
        Relationship.from_triple("document:readme", "view", "user:alice"),
        timeout=5.0,
    )
    print(f"user:alice can view document:readme: {result.has_permission}")
    assert result.has_permission is True

    await client.delete_relationships(
        Filter(resource_type="document", resource_id="readme")
    )


async def test_bulk_import_is_not_bounded_by_the_unary_default(client: SpiceDBClient):
    # import_relationships (ImportBulkRelationships) is client-streaming: its
    # duration scales with the size of the caller's dataset, not with server
    # latency, so it is explicitly excluded from default_timeout. Calling it
    # with no `timeout` at all -- as below -- must still succeed; if a future
    # change accidentally routed the unary default into this call, a large
    # enough import would start failing with DeadlineExceeded well before it
    # finished.
    users = [f"user{i}" for i in range(50)]
    relationships = [
        Relationship.from_triple("document:bulk", "viewer", f"user:{u}") for u in users
    ]
    num_loaded = await client.import_relationships(relationships)
    print(f"imported {num_loaded} relationships with no timeout bound")
    assert num_loaded == len(users)

    # A caller-supplied timeout on the same client-streaming call must still
    # be honored -- the exclusion is from the *default*, not from the ability
    # to bound the call at all.
    more_relationships = [
        Relationship.from_triple("document:bulk2", "viewer", f"user:{u}") for u in users
    ]
    num_loaded_bounded = await client.import_relationships(
        more_relationships, timeout=30.0
    )
    print(f"imported {num_loaded_bounded} relationships with an explicit timeout")
    assert num_loaded_bounded == len(users)

    await client.delete_relationships(
        Filter(resource_type="document", resource_id="bulk")
    )
