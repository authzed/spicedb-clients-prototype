"""Both flavors must be reached explicitly; neither owns the bare namespace."""

import pytest


def test_top_level_no_longer_exports_a_client():
    import spicedb

    assert not hasattr(spicedb, "SpiceDBClient"), (
        "spicedb.SpiceDBClient must not exist -- callers pick spicedb.sync or "
        "spicedb.aio explicitly (spec D1)"
    )
    assert "SpiceDBClient" not in spicedb.__all__


def test_aio_client_importable():
    from spicedb.aio import SpiceDBClient

    assert SpiceDBClient.__name__ == "SpiceDBClient"


def test_shared_vocabulary_stays_top_level():
    """Types/consistency/errors are flavor-free and must NOT move."""
    import spicedb

    for name in (
        "Relationship", "Filter", "Transaction", "LookupResource",
        "LookupSubject", "Permissionship", "SpiceDBError", "full", "at_least",
    ):
        assert hasattr(spicedb, name), f"{name} must remain importable from spicedb"


@pytest.mark.parametrize("flavor", ["aio"])
def test_flavor_module_exports_only_the_client(flavor):
    mod = __import__(f"spicedb.{flavor}", fromlist=["SpiceDBClient"])
    assert mod.__all__ == ["SpiceDBClient"]
