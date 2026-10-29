"""Example: testing authorization code with the in-memory double.

Code written against ``spicedb.protocols.AsyncClientProtocol`` runs unchanged
on the real client and on ``spicedb.testing.AsyncInMemoryClient``. The double
resolves a check from a registered result, then a resolver callback, then the
stored relationships (a relationship whose relation is the checked name), and
serves lookups over the objects it knows. It evaluates no schema, so this
example asks about relations, where the double and SpiceDB must agree, and
then shows a resolver standing in for a computed permission.
"""

import pytest
from spicedb import (
    Filter,
    ObjectRef,
    PermissionCheck,
    Relationship,
    SubjectRef,
    Transaction,
    full,
)
from spicedb.aio import SpiceDBClient
from spicedb.protocols import AsyncClientProtocol
from spicedb.testing import AsyncInMemoryClient

pytestmark = pytest.mark.integration

FIXTURE = [
    "document:readme#viewer@user:alice",
    "document:design#viewer@user:alice",
    "document:secret#viewer@user:bob",
    "document:design#editor@user:carol",
]


def alice() -> SubjectRef:
    return SubjectRef(ObjectRef("user", "alice"))


async def what_alice_sees(client: AsyncClientProtocol) -> tuple[list[str], list[str], bool]:
    """Application code: typed against the protocol, not a concrete client."""
    docs = sorted([
        r.resource_id async for r in client.lookup_resources(
            resource_type="document", permission="viewer", subject=alice(), consistency=full(),
        )
    ])
    viewers = sorted([
        s.subject.subject_id async for s in client.lookup_subjects(
            resource=ObjectRef("document", "design"), permission="viewer", subject_type="user",
            consistency=full(),
        )
    ])
    check = PermissionCheck(ObjectRef("document", "secret"), "viewer", alice())
    return docs, viewers, (await client.check(check, consistency=full())).has_permission


async def test_double_agrees_with_spicedb_on_relations(client: SpiceDBClient):
    relationships = [Relationship.from_tuple(t) for t in FIXTURE]
    txn = Transaction()
    for r in relationships:
        txn.touch(r)
    await client.write(txn)
    try:
        real = await what_alice_sees(client)
    finally:
        await client.delete_all_relationships_non_atomic(Filter(resource_type="document"))

    double = AsyncInMemoryClient(relationships)
    fake = await what_alice_sees(double)
    print(f"SpiceDB: {real}\ndouble:  {fake}")
    assert real == fake == (["design", "readme"], ["alice"], False)


async def test_resolver_stands_in_for_a_computed_permission():
    relationships = [Relationship.from_tuple(t) for t in FIXTURE]
    double = AsyncInMemoryClient(relationships)
    # "view" is computed by the schema (viewer + editor + owner); the double
    # evaluates no schema, so a resolver supplies the rule under test. A
    # resolver must not call the double, so it closes over the fixture.
    double.set_check_resolver(
        lambda c: c.permission == "view" and any(
            r.resource == c.resource and r.relation in ("viewer", "editor", "owner")
            and r.subject == c.subject for r in relationships
        ) or None
    )
    carol = SubjectRef(ObjectRef("user", "carol"))
    docs = [r.resource_id async for r in double.lookup_resources(
        resource_type="document", permission="view", subject=carol, consistency=full(),
    )]
    assert docs == ["design"]
