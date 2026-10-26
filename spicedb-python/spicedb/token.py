"""Opaque native SpiceDB revision."""
from dataclasses import dataclass
from spicedb.errors import InvalidArgumentError

@dataclass(frozen=True, slots=True, init=False)
class ZedToken:
    """An opaque revision; construct from a string and serialize with str()."""
    _value: str

    def __init__(self, value: str) -> None:
        if not isinstance(value, str):
            raise InvalidArgumentError("ZedToken: expected string")
        object.__setattr__(self, "_value", value)

    def __str__(self) -> str:
        return self._value

def _token_string(value: ZedToken) -> str:
    if not isinstance(value, ZedToken):
        raise InvalidArgumentError("revision: expected ZedToken")
    return str(value)
