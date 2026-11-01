"""Clock port protocol for deterministic time control."""

from datetime import datetime
from typing import Protocol


class ClockPort(Protocol):
    def now(self) -> datetime:
        """Return current datetime."""
        ...

    def sleep(self, seconds: float) -> None:
        """Pause execution."""
        ...
