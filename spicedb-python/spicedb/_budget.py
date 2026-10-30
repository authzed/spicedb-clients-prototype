"""One monotonic deadline shared by all work in a public operation."""

import math
import time
from spicedb.errors import LocalValidationError, DeadlineExceededError


class Budget:
    def __init__(self, timeout):
        if timeout is not None and (
            not isinstance(timeout, (int, float))
            or isinstance(timeout, bool)
            or not math.isfinite(timeout)
            or timeout <= 0
        ):
            raise LocalValidationError("timeout: expected a finite positive number")
        self.deadline = None if timeout is None else time.monotonic() + timeout

    def remaining(self):
        if self.deadline is None:
            return None
        remaining = self.deadline - time.monotonic()
        if remaining <= 0:
            raise DeadlineExceededError("operation deadline exceeded")
        return remaining

    def delay(self, seconds):
        remaining = self.remaining()
        return min(seconds, remaining) if remaining is not None else seconds
