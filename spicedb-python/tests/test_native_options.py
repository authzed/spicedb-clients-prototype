"""Native token and enum contracts, including rejection before transport work."""
from dataclasses import FrozenInstanceError
from unittest.mock import Mock
import pytest
from spicedb import (
    ZedToken, Tracing, LookupDebug, Wildcards, WatchContent,
    TransportSecurity, RemoteCredentials, InvalidArgumentError,
    ObjectRef, SubjectRef, full, at_least,
)
from spicedb import _requests
from spicedb.sync import SpiceDBClient
from spicedb.aio import SpiceDBClient as AioClient

@pytest.mark.parametrize("value", ["", "  opaque\x00token\n", "☃/+="])
def test_token_lossless_immutable(value):
    token = ZedToken(value)
    assert str(token) == value
    assert token == ZedToken(value)
    assert hash(token) == hash(ZedToken(value))
    assert token != value
    with pytest.raises(FrozenInstanceError):
        token._value = "changed"


def test_token_validation():
    with pytest.raises(InvalidArgumentError):
        ZedToken(123)
    with pytest.raises(InvalidArgumentError):
        at_least("revision")
    assert at_least(ZedToken("opaque"))._to_proto().at_least_as_fresh.token == "opaque"


@pytest.mark.parametrize("client_type", [SpiceDBClient, AioClient])
@pytest.mark.parametrize("endpoint", ["remote.example:50051", "192.0.2.1:50051", "[2001:db8::1]:50051"])
def test_remote_credentials_named_opt_in(client_type, endpoint):
    with pytest.raises(InvalidArgumentError):
        client_type(endpoint, token="secret", transport_security=TransportSecurity.PLAINTEXT)
    client = client_type(endpoint, token="secret", transport_security=TransportSecurity.PLAINTEXT, remote_credentials=RemoteCredentials.ALLOW)
    assert client._insecure
    assert client._metadata == [("authorization", "Bearer secret")]


@pytest.mark.parametrize("kwargs", [{"transport_security": True}, {"remote_credentials": "allow"}])
def test_invalid_transport_options(kwargs):
    with pytest.raises(InvalidArgumentError):
        SpiceDBClient("remote.example:50051", token="secret", **kwargs)


def test_watch_content_combines_all_kinds():
    request = _requests.watch_request((), ZedToken("resume"), tuple(WatchContent))
    assert set(request.optional_update_kinds) == {1, 2, 3}
    assert request.optional_start_cursor.token == "resume"
    with pytest.raises(InvalidArgumentError):
        _requests.watch_request((), None, ["checkpoints"])


def test_invalid_options_reject_before_transport():
    client = SpiceDBClient("localhost:50051", token="secret")
    client._ensure_channel = Mock(side_effect=AssertionError("transport reached"))
    with pytest.raises(InvalidArgumentError):
        client.check_bulk([], consistency=full(), tracing=True)
    with pytest.raises(InvalidArgumentError):
        client.lookup_resources(resource_type="doc", permission="view", subject=SubjectRef(ObjectRef("user", "one")), consistency=full(), debug=True)
    with pytest.raises(InvalidArgumentError):
        client.lookup_subjects(resource=ObjectRef("doc", "one"), permission="view", subject_type="user", consistency=full(), wildcards=True)


def test_mode_values_are_distinct_enums():
    assert Tracing.ENABLED is not LookupDebug.ENABLED
    assert Wildcards.DEFAULT.value == 0
