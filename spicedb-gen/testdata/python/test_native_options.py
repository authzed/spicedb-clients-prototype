"""Generated factories and check forwarding require native modes and tokens."""
from unittest.mock import Mock, AsyncMock
import pytest
from spicedb import (
    WriteResult, ZedToken, TransportSecurity, RemoteCredentials, Tracing,
    InvalidArgumentError, CheckResult, Permissionship, full,
)
from testdata.sync import TypedClient
from testdata.aio import TypedClient as AsyncTypedClient
from testdata.permissions import Document, User

@pytest.mark.parametrize("factory", [TypedClient.connect, AsyncTypedClient.connect])
def test_generated_remote_plaintext_policy(factory):
    with pytest.raises(InvalidArgumentError):
        factory("remote.example:50051", "secret", transport_security=TransportSecurity.PLAINTEXT)
    client = factory("remote.example:50051", "secret", transport_security=TransportSecurity.PLAINTEXT, remote_credentials=RemoteCredentials.ALLOW)
    assert client.client._insecure
    assert client.client._metadata == [("authorization", "Bearer secret")]


def test_generated_check_forwards_mode_and_result():
    result = CheckResult(Permissionship.HAS_PERMISSION, (), ZedToken("checked"))
    native = Mock()
    native.check.return_value = result
    client = TypedClient(native)
    assert client.check(full(), Document("one").view, User("alice"), tracing=Tracing.ENABLED) is result
    assert native.check.call_args.kwargs["tracing"] is Tracing.ENABLED
    native.write.return_value = WriteResult(ZedToken("written"))
    assert client.touch(Document("one").viewer(User("alice"))).revision == ZedToken("written")


async def test_async_generated_check_forwards_mode_and_result():
    result = CheckResult(Permissionship.HAS_PERMISSION, (), ZedToken("checked"))
    native = Mock()
    native.check = AsyncMock(return_value=result)
    client = AsyncTypedClient(native)
    assert await client.check(full(), Document("one").view, User("alice"), tracing=Tracing.ENABLED) is result
    assert native.check.call_args.kwargs["tracing"] is Tracing.ENABLED


@pytest.mark.parametrize("wrapper", [TypedClient, AsyncTypedClient])
def test_generated_lookup_forwards_filter(wrapper):
    from spicedb import LookupPermissionshipFilter
    from testdata.permissions import DOCUMENT_VIEW
    native = Mock()
    client = wrapper(native)
    client.lookup_resources(full(), DOCUMENT_VIEW, User("alice"), permissionship_filter=LookupPermissionshipFilter.HAS_PERMISSION_ONLY)
    assert native.lookup_resources.call_args.kwargs["permissionship_filter"] is LookupPermissionshipFilter.HAS_PERMISSION_ONLY


@pytest.mark.parametrize("wrapper", [TypedClient, AsyncTypedClient])
@pytest.mark.parametrize("method", ["touch", "create", "delete", "write"])
async def test_generated_writes_preserve_result(wrapper, method):
    from testdata.permissions import TypedTransaction
    result = WriteResult(ZedToken(" opaque\x00☃ "))
    native = Mock()
    native.write = AsyncMock(return_value=result) if wrapper is AsyncTypedClient else Mock(return_value=result)
    client = wrapper(native)
    argument = TypedTransaction() if method == "write" else Document("one").viewer(User("alice"))
    returned = getattr(client, method)(argument, timeout=5)
    if wrapper is AsyncTypedClient:
        returned = await returned
    assert returned is result
    assert str(returned.revision) == " opaque\x00☃ "
    assert native.write.call_args.kwargs["timeout"] == 5
