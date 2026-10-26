"""Named choices for client behavior."""
from enum import Enum
from spicedb.errors import InvalidArgumentError

class Tracing(Enum):
    DISABLED = False
    ENABLED = True

class LookupPermissionshipFilter(Enum):
    ALL = "all"
    HAS_PERMISSION_ONLY = "has_permission_only"

class LookupDebug(Enum):
    DISABLED = False
    ENABLED = True

class Wildcards(Enum):
    DEFAULT = 0
    INCLUDE = 1
    EXCLUDE = 2

class WatchContent(Enum):
    RELATIONSHIPS = 1
    SCHEMA = 2
    CHECKPOINTS = 3

class TransportSecurity(Enum):
    TLS = "tls"
    PLAINTEXT = "plaintext"

class RemoteCredentials(Enum):
    REJECT = "reject"
    ALLOW = "allow"

def _option(value, kind):
    if not isinstance(value, kind):
        raise InvalidArgumentError(f"expected {kind.__name__}, got {value!r}")
    return value.value
