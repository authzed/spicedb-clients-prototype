"""Example: Call deadlines.

Demonstrates the client-level `default_timeout` construction parameter, a
per-call `timeout` override on a unary call, and that bulk import
(`import_relationships`) is a client-streaming call that is NOT bounded by
`default_timeout` -- see root DESIGN.md, "RULE: A unary call must have a
deadline".

The failure that rule exists to close is a *wedged* server: one that accepts
the connection and then never answers. Nothing looks wrong at the transport
level, so an unbounded call hangs forever rather than erroring. The tests that
call a real SpiceDB below pass identically whether or not the timeout ever
reaches the wire, so the last two stand up a socket that behaves exactly that
way and require the call to come back `DeadlineExceededError` on the caller's
schedule.
"""

import asyncio
import socket
import time

import pytest

from conftest import ENDPOINT, TOKEN, clear_documents
from spicedb import Filter, Relationship, full
from spicedb.aio import SpiceDBClient
from spicedb.errors import DeadlineExceededError
from spicedb.types import Transaction


pytestmark = pytest.mark.integration


# The deadline handed to the calls against the wedged server. Short, because
# the point is to watch it expire.
WEDGED_TIMEOUT = 2.0

# Wall-clock bound on a wedged call. If a call with a 2s deadline has not
# returned after this long, the deadline is not reaching the RPC -- and the
# example fails with that message instead of hanging the CI job.
WATCHDOG = 17.0


def _wedged_listener() -> socket.socket:
    """A socket that accepts TCP connections and never speaks gRPC.

    The kernel completes the TCP handshake for connections sitting in the
    backlog, so a client connects successfully and then waits forever for the
    HTTP/2 server preface. That is what a wedged SpiceDB looks like from a
    client -- an open, healthy-looking connection with no reply behind it --
    and it is why "the connection worked" is not a bound.
    """
    listener = socket.socket()
    listener.bind(("127.0.0.1", 0))
    listener.listen(1)
    return listener


async def test_default_timeout_construction_param():
    # default_timeout applies to every unary call that doesn't pass its own
    # `timeout` override. This is the documented, real construction path --
    # not a mock -- so a signature drift here (e.g. the parameter silently
    # disappearing from the constructor) would fail this test, not just a
    # unit test against a stalling stub.
    async with SpiceDBClient(
        ENDPOINT,
        token=TOKEN,
        insecure=True,
        default_timeout=5.0,
    ) as client:
        # Clear before writing the schema -- see conftest.clear_documents.
        await clear_documents(client)
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


async def test_default_timeout_expires_against_a_server_that_never_answers():
    listener = _wedged_listener()
    port = listener.getsockname()[1]
    try:
        async with SpiceDBClient(
            f"127.0.0.1:{port}",
            token=TOKEN,
            insecure=True,
            default_timeout=WEDGED_TIMEOUT,
        ) as wedged:
            started = time.monotonic()
            try:
                result = await asyncio.wait_for(
                    wedged.check_permission(
                        full(),
                        Relationship.from_triple(
                            "document:readme", "view", "user:alice"
                        ),
                    ),
                    timeout=WATCHDOG,
                )
            except DeadlineExceededError:
                # The specific error matters. "Some exception was raised" is
                # also satisfied by UnavailableError from a refused
                # connection -- which is what this would degrade into if the
                # listener stopped accepting, and which says nothing at all
                # about deadlines.
                elapsed = time.monotonic() - started
                print(f"wedged server: DeadlineExceededError after {elapsed:.2f}s")
                assert elapsed < WATCHDOG
            except TimeoutError:
                pytest.fail(
                    f"a call with a {WEDGED_TIMEOUT}s default_timeout had not "
                    f"returned after {WATCHDOG}s against a server that never "
                    "answers: the deadline is not reaching the RPC"
                )
            else:
                pytest.fail(
                    f"expected DeadlineExceededError, got a result: {result!r}"
                )
    finally:
        listener.close()


async def test_per_call_timeout_expires_against_a_server_that_never_answers():
    """A per-call `timeout=` has to bite the same way the client default does:
    the override is a different code path, and one that accepted the argument
    and dropped it would still pass every fast-local-call test above."""
    listener = _wedged_listener()
    port = listener.getsockname()[1]
    try:
        # No default_timeout at all here, so only the per-call argument can
        # bound this.
        async with SpiceDBClient(
            f"127.0.0.1:{port}", token=TOKEN, insecure=True
        ) as wedged:
            try:
                await asyncio.wait_for(
                    wedged.check_permission(
                        full(),
                        Relationship.from_triple(
                            "document:readme", "view", "user:alice"
                        ),
                        timeout=WEDGED_TIMEOUT,
                    ),
                    timeout=WATCHDOG,
                )
            except DeadlineExceededError:
                pass
            except TimeoutError:
                pytest.fail(
                    f"a call with a {WEDGED_TIMEOUT}s per-call timeout had not "
                    f"returned after {WATCHDOG}s against a server that never "
                    "answers: the per-call timeout is not reaching the RPC"
                )
            else:
                pytest.fail("expected DeadlineExceededError from a wedged server")
    finally:
        listener.close()
