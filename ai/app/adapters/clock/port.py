"""Clock port protocol for deterministic time control."""

from datetime import datetime
from typing import Protocol, runtime_checkable


@runtime_checkable
class ClockPort(Protocol):
    """Protocol defining clock time operations."""

    def now(self) -> datetime:
        """Return current datetime."""
        ...

    def sleep(self, seconds: float) -> None:
        """Pause execution."""
        ...
