"""Unit tests for spicedb.consistency — no SpiceDB instance needed."""

from spicedb.consistency import (
    at_least,
    at_least_or_full,
    at_least_or_min_latency,
    full,
    min_latency,
    snapshot,
)


def test_full():
    c = full()
    assert c.fully_consistent is True


def test_min_latency():
    c = min_latency()
    assert c.minimize_latency is True


def test_at_least():
    c = at_least("sometoken123")
    assert c.at_least_as_fresh.token == "sometoken123"


def test_at_least_or_full_with_revision():
    c = at_least_or_full("sometoken123")
    assert c.at_least_as_fresh.token == "sometoken123"


def test_at_least_or_full_empty():
    c = at_least_or_full("")
    assert c.fully_consistent is True


def test_at_least_or_min_latency_with_revision():
    c = at_least_or_min_latency("sometoken123")
    assert c.at_least_as_fresh.token == "sometoken123"


def test_at_least_or_min_latency_empty():
    c = at_least_or_min_latency("")
    assert c.minimize_latency is True


def test_snapshot():
    c = snapshot("sometoken123")
    assert c.at_exact_snapshot.token == "sometoken123"
