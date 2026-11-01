"""Deterministic fake clock adapter for repeatable tests."""

from datetime import datetime, timedelta, timezone

from .port import ClockPort


class FakeClockAdapter(ClockPort):
    def __init__(self, start_time: datetime | None = None):
        if start_time is None:
            self._current_time = datetime(2026, 11, 1, 12, 0, 0, tzinfo=timezone.utc)
        else:
            self._current_time = start_time

    def now(self) -> datetime:
        return self._current_time

    def sleep(self, seconds: float) -> None:
        self.advance(timedelta(seconds=seconds))

    def advance(self, dt: timedelta) -> None:
        self._current_time += dt
