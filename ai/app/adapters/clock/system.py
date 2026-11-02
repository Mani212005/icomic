"""System clock adapter."""

import time
from datetime import datetime, timezone

from .port import ClockPort


class SystemClockAdapter(ClockPort):
    """Real clock adapter delegating to system time."""

    def now(self) -> datetime:
        return datetime.now(timezone.utc)

    def sleep(self, seconds: float) -> None:
        time.sleep(seconds)
