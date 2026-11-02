"""Redis queue adapter stub."""

from typing import Any

from .port import JobEnvelope, QueuePort


class RedisQueueAdapter(QueuePort):
    """Redis background queue adapter stub.

    Deferred until live infrastructure deployment.
    """

    def __init__(self, *args, **kwargs):
        raise RuntimeError(
            "RedisQueueAdapter is not configured: live Redis queue integration is deferred "
            "until cloud deployment. Use FakeQueueAdapter (default) via ADAPTER_QUEUE=fake."
        )

    def enqueue(self, queue: str, payload: dict[str, Any], idempotency_key: str) -> str:
        raise NotImplementedError("Redis live queue adapter requires running Redis instance.")

    def dequeue(self, queue: str, timeout_s: int) -> JobEnvelope | None:
        raise NotImplementedError("Redis live queue adapter requires running Redis instance.")

    def ack(self, envelope: JobEnvelope) -> None:
        raise NotImplementedError("Redis live queue adapter requires running Redis instance.")

    def nack(self, envelope: JobEnvelope, delay_s: int) -> None:
        raise NotImplementedError("Redis live queue adapter requires running Redis instance.")
