"""Bearer-token metadata, shared by both client flavors.

Both clients attach the token by passing this metadata on every RPC. Neither
uses a gRPC interceptor, and neither composes `access_token_call_credentials`.

Why (spec D8): the two libraries disagree about interceptors in ways that
silently change behavior. `grpc.aio` sorts a multi-ABC interceptor into ONE
call-type bucket via an elif chain, so it covers unary-unary only; sync's
`intercept_channel` wraps per interceptor and fires on every call type. Layering
call credentials on top of either duplicates the header. Passing metadata
per call is uniform, explicit, and immune to both quirks.
"""

from __future__ import annotations


def bearer_metadata(token: str) -> list[tuple[str, str]]:
    """Build the gRPC metadata carrying the bearer token."""
    return [("authorization", f"Bearer {token}")]
