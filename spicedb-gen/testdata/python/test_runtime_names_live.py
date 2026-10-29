"""Runtime-name APIs against a live SpiceDB, both flavors: objects, subject
sets and relationships built from strings, checks and lookups by name.

Requires SpiceDB at SPICEDB_ENDPOINT (default localhost:50051).
"""

from __future__ import annotations

import os
import uuid
from pathlib import Path

import pytest
from spicedb import BulkItemErrors, Permissionship, TransportSecurity, full

from testdata.aio import TypedClient as AsyncTypedClient
from testdata.permissions import (
    TypedTransaction,
    parse_object,
    relationship_from_triple,
    subject_set_by_name,
)
from testdata.sync import TypedClient

SCHEMA = (Path(__file__).resolve().parents[1] / "sample.zed").read_text()
ENDPOINT = os.environ.get("SPICEDB_ENDPOINT", "localhost:50051")
TOKEN = os.environ.get("SPICEDB_TOKEN", "somerandomkeyhere")


def _ids() -> tuple[str, str, str, str]:
    n = uuid.uuid4().hex[:12]
    return f"doc_{n}", f"alice_{n}", f"bob_{n}", f"eng_{n}"


@pytest.mark.integration
def test_sync_runtime_names_live() -> None:
    doc, alice, bob, eng = _ids()
    with TypedClient.connect(ENDPOINT, TOKEN, transport_security=TransportSecurity.PLAINTEXT) as tc:
        tc.write_schema(SCHEMA)
        written = tc.write_txn(
            TypedTransaction().touch(
                relationship_from_triple(f"document:{doc}", "viewer", f"user:{alice}"),
                relationship_from_triple(f"document:{doc}", "viewer", f"team:{eng}#member"),
                relationship_from_triple(f"team:{eng}", "member", f"user:{bob}"),
            )
        )
        assert str(written.revision)

        assert tc.check_by_name(full(), "document", doc, "view", parse_object(f"user:{alice}")).has_permission
        assert tc.check_by_name(full(), "document", doc, "viewer", parse_object(f"user:{bob}")).has_permission
        assert not tc.check_by_name(full(), "document", doc, "edit", parse_object(f"user:{alice}")).has_permission
        assert tc.check_by_name(
            full(), "document", doc, "view", subject_set_by_name("team", eng, "member")
        ).has_permission

        results = tc.check_bulk_by_name(
            full(),
            [
                ("document", doc, "view", parse_object(f"user:{alice}")),
                ("document", doc, "edit", parse_object(f"user:{alice}")),
                ("document", doc, "view", parse_object(f"user:{bob}")),
            ],
            item_errors=BulkItemErrors.RETURN,
        )
        assert [r.permissionship for r in results] == [
            Permissionship.HAS_PERMISSION,
            Permissionship.NO_PERMISSION,
            Permissionship.HAS_PERMISSION,
        ]

        # Relations are looked up as well as permissions.
        with tc.lookup_resources_by_name(full(), "document", "viewer", parse_object(f"user:{alice}")) as stream:
            assert [r.resource_id for r in stream] == [doc]
        with tc.lookup_subjects_by_name(full(), "document", doc, "view", "user") as stream:
            assert sorted(s.subject.subject_id for s in stream) == sorted([alice, bob])
        with tc.lookup_subjects_by_name(full(), "document", doc, "viewer", "team", "member") as stream:
            # team#member is recursive, so SpiceDB may report the set more than once.
            assert {s.subject.subject_id for s in stream} == {eng}
        # An unreachable subject type has no results, as SpiceDB answers.
        assert list(tc.lookup_resources_by_name(full(), "document", "view", parse_object("organization:acme"))) == []
        assert list(tc.lookup_subjects_by_name(full(), "document", doc, "view", "organization")) == []


@pytest.mark.integration
async def test_async_runtime_names_live() -> None:
    doc, alice, bob, eng = _ids()
    async with AsyncTypedClient.connect(ENDPOINT, TOKEN, transport_security=TransportSecurity.PLAINTEXT) as tc:
        await tc.write_schema(SCHEMA)
        await tc.touch(
            relationship_from_triple(f"document:{doc}", "owner", f"user:{alice}"),
            relationship_from_triple(f"team:{eng}", "member", f"user:{bob}"),
        )
        assert (await tc.check_by_name(full(), "document", doc, "delete", parse_object(f"user:{alice}"))).has_permission
        results = await tc.check_bulk_by_name(
            full(),
            [
                ("document", doc, "edit", parse_object(f"user:{alice}")),
                ("document", doc, "view", parse_object(f"user:{bob}")),
            ],
        )
        assert [r.has_permission for r in results] == [True, False]
        found = [
            r.resource_id
            async for r in tc.lookup_resources_by_name(full(), "document", "owner", parse_object(f"user:{alice}"))
        ]
        assert found == [doc]
        subjects = [s.subject.subject_id async for s in tc.lookup_subjects_by_name(full(), "team", eng, "member", "user")]
        assert subjects == [bob]
        assert [r async for r in tc.lookup_resources_by_name(full(), "document", "view", parse_object("team:x"))] == []
