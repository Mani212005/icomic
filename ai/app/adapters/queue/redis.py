"""Redis queue adapter."""

import os
from typing import Any

from .port import JobEnvelope, QueuePort


class RedisQueueAdapter(QueuePort):
    def __init__(self, redis_url: str | None = None):
        self.redis_url = redis_url or os.getenv("REDIS_URL", "redis://localhost:6379/0")

    def enqueue(self, queue: str, payload: dict[str, Any], idempotency_key: str) -> str:
        raise NotImplementedError("Redis live queue adapter requires running Redis instance.")

    def dequeue(self, queue: str, timeout_s: int) -> JobEnvelope | None:
        raise NotImplementedError("Redis live queue adapter requires running Redis instance.")

    def ack(self, envelope: JobEnvelope) -> None:
        raise NotImplementedError("Redis live queue adapter requires running Redis instance.")

    def nack(self, envelope: JobEnvelope, delay_s: int) -> None:
        raise NotImplementedError("Redis live queue adapter requires running Redis instance.")
