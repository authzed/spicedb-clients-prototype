"""Example: the opt-in a plaintext connection to a remote host requires.

Root DESIGN.md, "RULE: Credentials over insecure transport require an explicit
opt-in".

The failure this rule exists to prevent is mundane and common: a developer
copies ``insecure=True`` out of a localhost example into a staging config, and a
long-lived SpiceDB token -- a complete authorization bypass in anyone else's
hands -- goes onto the wire in cleartext with nothing signalling that it
happened. So ``insecure=True`` alone is loopback-only, and reaching a remote host
over plaintext takes a second, separately-named parameter the caller cannot
supply by accident: ``allow_insecure_remote_credentials``.

The sharpest case is the last one. The rule requires the guard to fail closed on
any endpoint whose authority could move under URI parsing: given
``127.0.0.1:443@evil.com``, a last-colon split reads the host as ``127.0.0.1``
and waves it through, while the authority is really ``evil.com``. Python hands
its target to gRPC's C-core, which parses it in C++ out of reach, so the rule
requires this client to refuse such endpoints outright rather than guess.
"""

import pytest

from conftest import ENDPOINT, TOKEN
from spicedb.aio import SpiceDBClient
from spicedb.errors import InvalidArgumentError


pytestmark = pytest.mark.integration


async def test_loopback_plaintext_needs_no_opt_in() -> None:
    """The case the rule deliberately leaves ergonomic.

    A token on a loopback socket never leaves the machine, so requiring ceremony
    here would only train developers to reach for the opt-in reflexively.
    """
    async with SpiceDBClient(ENDPOINT, token=TOKEN, insecure=True) as client:
        # Prove the client is usable, not merely constructed: construction is
        # lazy, so a constructor returning a client that could not talk to
        # anything would still satisfy a "did not raise" assertion.
        await client.write_schema(
            "definition user {}\n\n"
            "definition document {\n"
            "    relation viewer: user\n"
            "    permission view = viewer\n"
            "}\n"
        )


async def test_remote_plaintext_is_refused_without_the_opt_in() -> None:
    """No connection is attempted: the refusal happens at construction.

    This is not about whether the host exists -- example.com is refused because
    it is not loopback, full stop.
    """
    # This client's own typed argument error, the same one a filter the wire
    # cannot express uses -- see root DESIGN.md, "RULE: Credentials over insecure
    # transport require an explicit opt-in", clause 4.
    with pytest.raises(InvalidArgumentError) as excinfo:
        SpiceDBClient("example.com:50051", token=TOKEN, insecure=True)
    assert "loopback" in str(excinfo.value).lower()


async def test_remote_plaintext_is_allowed_with_the_named_opt_in() -> None:
    """Two parameters, not one, and that separation is the point.

    ``insecure`` selects the plaintext transport;
    ``allow_insecure_remote_credentials`` accepts the credential exposure that
    follows. "I want plaintext for local dev" and "I accept shipping this token
    in cleartext to a remote host" are different decisions, and clause 1 forbids
    one boolean from doing both jobs.
    """
    client = SpiceDBClient(
        "example.com:50051",
        token=TOKEN,
        insecure=True,
        allow_insecure_remote_credentials=True,
    )
    await client.close()


@pytest.mark.parametrize(
    "endpoint",
    [
        "127.0.0.1:443@evil.com",
        "127.0.0.1:50051/../evil.com",
        "127.0.0.1:50051?x=evil.com",
        "127.0.0.1:50051#evil.com",
        "127.0.0.1 :50051",
    ],
)
async def test_authority_moving_endpoints_are_refused(endpoint: str) -> None:
    """Fail closed on anything that could move the authority under URI parsing.

    A client that split on the last colon would call ``127.0.0.1:443@evil.com``
    loopback and hand the token to evil.com. Python cannot ask its transport's
    parser -- gRPC's C-core parses the target in C++ -- so the rule requires
    refusing these outright instead of guessing.
    """
    with pytest.raises(InvalidArgumentError):
        SpiceDBClient(endpoint, token=TOKEN, insecure=True)
