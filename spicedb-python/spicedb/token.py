"""Opaque native SpiceDB revision."""
from dataclasses import dataclass
from spicedb.errors import LocalValidationError

@dataclass(frozen=True, slots=True, init=False)
class ZedToken:
    """An opaque revision; construct from a string and serialize with str()."""
    _value: str

    def __init__(self, value: str) -> None:
        if not isinstance(value, str):
            raise LocalValidationError("ZedToken: expected string")
        object.__setattr__(self, "_value", value)

    def __str__(self) -> str:
        return self._value

def _token_string(value: ZedToken) -> str:
    if not isinstance(value, ZedToken):
        raise LocalValidationError("revision: expected ZedToken")
    return str(value)


def _is_blank(value: ZedToken) -> bool:
    """Whether ``value`` carries no revision: empty or only whitespace."""
    return not _token_string(value).strip()


def _revision_string(value: ZedToken, what: str) -> str:
    """The token's string for a caller-supplied revision input.

    An empty or whitespace-only token (usually a blank stored value) is a
    local validation error, never a request the server rejects. See root
    DESIGN.md, "Reject unusable credentials and trust material at
    construction".
    """
    if _is_blank(value):
        raise LocalValidationError(f"{what}: expected nonempty ZedToken, got {str(value)!r}")
    return str(value)
