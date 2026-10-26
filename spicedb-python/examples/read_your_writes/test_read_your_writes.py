"""Example: Read-your-writes token round-trip (checked_at / looked_up_at).

CheckPermissionResponse.checked_at and LookupResourcesResponse/
LookupSubjectsResponse.looked_up_at exist so a caller can pin a later call to
"at least as fresh as this earlier write/check/lookup" via at_least() --
before this client exposed them, the tokens existed on the wire but were
dropped by every mapper. This example demonstrates the intended calling
pattern end-to-end: write a relationship, take the returned result.revision,
and thread it through at_least() into a subsequent check/lookup
without error -- and that checked_at/looked_up_at come back populated on the
response, so they're actually there to thread further.

What this example does NOT prove: that at_least() enforces freshness.
docker-compose.test.yml runs SpiceDB against the in-memory datastore, where
every read is trivially fully consistent regardless of the consistency
requested -- this test would pass identically with min_latency(), full(), or
even a bogus token, so it cannot observe staleness protection on this
backend. The part of the chain that IS verified independent of any backend
-- that the token at_least() builds actually lands in the outgoing request's
consistency.at_least_as_fresh, rather than being silently dropped somewhere
between the Consistency object and the wire -- is a unit test:
tests/test_requests.py::test_check_bulk_request_carries_at_least_as_fresh_token
and its lookup_resources() counterpart.
"""

from spicedb import ObjectRef, SubjectRef, PermissionCheck
import pytest
from spicedb import Filter, Relationship, Permissionship
from spicedb.aio import SpiceDBClient
from spicedb.consistency import at_least
from spicedb.types import Transaction

pytestmark = pytest.mark.integration


async def test_check_observes_a_write_via_its_revision(client: SpiceDBClient):
    rel = Relationship.from_triple("document:rwdoc", "viewer", "user:alice")
    txn = Transaction()
    txn.touch(rel)
    written_at = (await client.write(txn)).revision
    print(f"wrote at revision: {written_at}")
    assert written_at
    result = await client.check(
        PermissionCheck(rel.resource, rel.relation, rel.subject),
        consistency=at_least(written_at),
    )
    print(
        f"alice can view document:rwdoc (at_least written_at): {result.has_permission}"
    )
    assert result.has_permission is True
    assert str(result.checked_at)
    await client.delete_all_relationships_non_atomic(
        Filter(resource_type="document", resource_id="rwdoc")
    )


async def test_lookup_resources_observes_a_write_via_its_revision(
    client: SpiceDBClient,
):
    rel = Relationship.from_triple("document:rwdoc2", "viewer", "user:alice")
    txn = Transaction()
    txn.touch(rel)
    written_at = (await client.write(txn)).revision
    assert written_at
    found = [
        r
        async for r in client.lookup_resources(
            resource_type="document",
            permission="view",
            subject=SubjectRef(ObjectRef("user", "alice"), None),
            consistency=at_least(written_at),
        )
        if r.resource_id == "rwdoc2" and r.permissionship is Permissionship.HAS_PERMISSION
    ]
    assert found, "lookup_resources() did not return the written resource"
    assert found[0].looked_up_at
    await client.delete_all_relationships_non_atomic(
        Filter(resource_type="document", resource_id="rwdoc2")
    )
