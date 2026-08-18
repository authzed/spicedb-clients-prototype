"""Unit tests for spicedb.consistency — no SpiceDB instance needed."""

from google.protobuf.message import Message

from spicedb.consistency import (
    Consistency,
    at_least,
    at_least_or_full,
    at_least_or_min_latency,
    full,
    min_latency,
    snapshot,
)


def test_consistency_is_not_a_proto_type():
    """Consistency must be an opaque native type, not the raw proto message.

    Guards against the NOT-1 violation where the public API leaked
    ``permission_service_pb2.Consistency`` directly.
    """
    c = full()
    assert isinstance(c, Consistency)
    assert not isinstance(c, Message)


def test_full():
    c = full()
    assert isinstance(c, Consistency)
    proto = c._to_proto()
    assert proto.fully_consistent is True


def test_min_latency():
    c = min_latency()
    assert isinstance(c, Consistency)
    proto = c._to_proto()
    assert proto.minimize_latency is True


def test_at_least():
    c = at_least("sometoken123")
    assert isinstance(c, Consistency)
    proto = c._to_proto()
    assert proto.at_least_as_fresh.token == "sometoken123"


def test_at_least_or_full_with_revision():
    c = at_least_or_full("sometoken123")
    assert isinstance(c, Consistency)
    proto = c._to_proto()
    assert proto.at_least_as_fresh.token == "sometoken123"


def test_at_least_or_full_empty():
    c = at_least_or_full("")
    assert isinstance(c, Consistency)
    proto = c._to_proto()
    assert proto.fully_consistent is True


def test_at_least_or_min_latency_with_revision():
    c = at_least_or_min_latency("sometoken123")
    assert isinstance(c, Consistency)
    proto = c._to_proto()
    assert proto.at_least_as_fresh.token == "sometoken123"


def test_at_least_or_min_latency_empty():
    c = at_least_or_min_latency("")
    assert isinstance(c, Consistency)
    proto = c._to_proto()
    assert proto.minimize_latency is True


def test_snapshot():
    c = snapshot("sometoken123")
    assert isinstance(c, Consistency)
    proto = c._to_proto()
    assert proto.at_exact_snapshot.token == "sometoken123"


def test_consistency_is_exported_from_package_root():
    """`Consistency` must be importable from `spicedb` directly (alongside
    the constructor functions) so callers can use it in type annotations,
    e.g. `def foo(c: Consistency) -> None: ...`."""
    import spicedb
    from spicedb import Consistency as ExportedConsistency

    assert ExportedConsistency is Consistency
    assert "Consistency" in spicedb.__all__
    assert isinstance(spicedb.full(), ExportedConsistency)
