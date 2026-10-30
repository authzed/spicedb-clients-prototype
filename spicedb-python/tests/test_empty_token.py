"""An empty or whitespace-only token is refused at construction.

Root DESIGN.md, "Reject unusable credentials and trust material at
construction": a blank token (usually an unset environment variable) is a
local validation error, never a bare ``Bearer`` header the server rejects on
the first call.
"""

from __future__ import annotations

import importlib

import grpc
import pytest

from spicedb import TransportSecurity
from spicedb.errors import LocalValidationError


@pytest.mark.parametrize("module", ["spicedb.sync", "spicedb.aio"])
@pytest.mark.parametrize("token", ["", " ", "\t\n"], ids=["empty", "space", "whitespace"])
@pytest.mark.parametrize("security", [TransportSecurity.TLS, TransportSecurity.PLAINTEXT])
def test_blank_token_is_refused_at_construction(module, token, security, monkeypatch):
    def explode(*args, **kwargs):
        raise AssertionError("a channel must not be created for a blank token")

    for name in ("insecure_channel", "secure_channel"):
        monkeypatch.setattr(grpc, name, explode)
        monkeypatch.setattr(grpc.aio, name, explode)
    cls = importlib.import_module(module).SpiceDBClient
    with pytest.raises(LocalValidationError, match="token must be a nonempty string"):
        cls("localhost:50116", token=token, transport_security=security)
