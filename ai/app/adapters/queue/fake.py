"""In-process synchronous fake queue adapter for hermetic tests and development."""

import hashlib
from collections import deque
from typing import Any

from .port import JobEnvelope, QueuePort


class FakeQueueAdapter(QueuePort):
    def __init__(self):
        self._queues: dict[str, deque[JobEnvelope]] = {}
        self._idempotency_map: dict[str, str] = {}
        self.acknowledged: list[str] = []
        self.nacked: list[str] = []

    def enqueue(self, queue: str, payload: dict[str, Any], idempotency_key: str) -> str:
        if idempotency_key in self._idempotency_map:
            return self._idempotency_map[idempotency_key]

        job_id = f"job-fake-{hashlib.sha256(idempotency_key.encode()).hexdigest()[:12]}"
        self._idempotency_map[idempotency_key] = job_id

        if queue not in self._queues:
            self._queues[queue] = deque()

        envelope = JobEnvelope(
            job_id=job_id,
            queue=queue,
            payload=payload,
            idempotency_key=idempotency_key,
            retry_count=0,
        )
        self._queues[queue].append(envelope)
        return job_id

    def dequeue(self, queue: str, timeout_s: int = 0) -> JobEnvelope | None:
        q = self._queues.get(queue)
        if q and len(q) > 0:
            return q.popleft()
        return None

    def ack(self, envelope: JobEnvelope) -> None:
        self.acknowledged.append(envelope.job_id)

    def nack(self, envelope: JobEnvelope, delay_s: int) -> None:
        self.nacked.append(envelope.job_id)
        envelope.retry_count += 1
        if envelope.queue not in self._queues:
            self._queues[envelope.queue] = deque()
        self._queues[envelope.queue].append(envelope)
