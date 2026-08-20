"""Escape hatch: the gRPC internals behind the idiomatic client.

Secondary API, deliberately. Root DESIGN.md's "What NOT To Do" keeps channels,
stubs and metadata out of the *primary* API and permits exactly this -- "escape
hatches for advanced use are acceptable as clearly marked secondary API" -- so
that a gap in the idiomatic surface has a workaround short of forking the
client. Reach for it when you need a SpiceDB RPC, field, or call option this
client does not wrap yet; prefer the idiomatic methods for everything else,
since nothing here maps errors, applies retries, or enforces a deadline.

**No stability promise beyond grpc's own.** :class:`RawGrpc` hands you objects
owned by ``grpcio`` and the buf-generated ``authzed.api.v1`` stubs; their
behavior is whatever those packages say it is, and this client will not shim
over a change in either. What this client does promise is that the objects are
the very ones it uses itself -- the same channel, carrying the same bearer
token -- not a second connection built behind your back.

**It is not a second way to connect.** There is no constructor here, and no way
to pass an endpoint, a token, or a TLS setting: :meth:`spicedb.sync.SpiceDBClient.raw_grpc`
and :meth:`spicedb.aio.SpiceDBClient.raw_grpc` only hand back what the client
already built. That is what keeps root DESIGN.md, "RULE: Credentials over
insecure transport require an explicit opt-in", in force -- the guard runs in
``__init__``, on the one path that constructs a channel, and this hatch cannot
route around it.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import TYPE_CHECKING

if TYPE_CHECKING:
    import grpc


@dataclass(frozen=True)
class RawGrpc:
    """The channel and call metadata a :class:`SpiceDBClient` is using.

    Build any stub you need from :attr:`channel` and pass :attr:`metadata` on
    every call -- this client authenticates per call rather than through
    channel credentials or an interceptor (see ``spicedb._auth``), so a stub
    built from :attr:`channel` alone sends no token and SpiceDB answers
    ``UNAUTHENTICATED``.

    Synchronous::

        from authzed.api.v1 import permission_service_pb2 as psp
        from authzed.api.v1 import permission_service_pb2_grpc as psg

        raw = client.raw_grpc()
        stub = psg.PermissionsServiceStub(raw.channel)
        resp = stub.CheckPermission(psp.CheckPermissionRequest(...), metadata=raw.metadata)

    Asynchronous -- ``raw_grpc()`` is awaited there because the channel is
    created on, and bound to, the running event loop::

        raw = await client.raw_grpc()
        stub = psg.PermissionsServiceStub(raw.channel)
        resp = await stub.CheckPermission(psp.CheckPermissionRequest(...), metadata=raw.metadata)

    Do not close :attr:`channel`: it belongs to the client that handed it to
    you, which closes it in ``close()``/``__exit__``. Closing it yourself
    breaks every subsequent idiomatic call on that client.
    """

    channel: grpc.Channel | grpc.aio.Channel
    """The live channel -- ``grpc.Channel`` from ``spicedb.sync``, ``grpc.aio.Channel``
    from ``spicedb.aio``. Both flavors' stubs are the same generated classes; only the
    call mechanics differ (``grpc.aio`` returns awaitables)."""

    metadata: tuple[tuple[str, str], ...]
    """The bearer-token metadata to pass as ``metadata=`` on every raw call. A
    tuple, not the client's own list, so mutating it cannot corrupt the metadata
    the idiomatic methods send."""
