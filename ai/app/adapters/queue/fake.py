"""In-process synchronous fake queue adapter for hermetic tests and development."""

import hashlib
from collections import deque
from copy import deepcopy
from threading import RLock
from typing import Any
from uuid import uuid4

from .port import JobEnvelope, QueuePort


class FakeQueueAdapter(QueuePort):
    """In-process synchronous FIFO queue with deduplication on idempotency key."""

    synchronous = True

    def __init__(self):
        self._queues: dict[str, deque[JobEnvelope]] = {}
        self._idempotency_map: dict[str, str] = {}
        self.acknowledged: list[str] = []
        self.nacked: list[str] = []
        self._jobs: dict[tuple[str, str], dict[str, Any]] = {}
        self._lock = RLock()

    def get_job(self, queue: str, idempotency_key: str) -> dict[str, Any] | None:
        with self._lock:
            return deepcopy(self._jobs.get((queue, idempotency_key)))

    def finish(self, envelope: JobEnvelope, result: dict[str, Any]) -> None:
        with self._lock:
            job = self._jobs[(envelope.queue, envelope.idempotency_key)]
            job.update(deepcopy(result))

    def enqueue(self, queue: str, payload: dict[str, Any], idempotency_key: str) -> str:
        with self._lock:
            return self._enqueue(queue, payload, idempotency_key)

    def _enqueue(self, queue: str, payload: dict[str, Any], idempotency_key: str) -> str:
        # Scope deduplication to the queue, just as the Redis adapter does.
        idempotency_key_map = f"{queue}:{idempotency_key}"
        if idempotency_key_map in self._idempotency_map:
            return self._idempotency_map[idempotency_key_map]

        job_id = f"job-fake-{hashlib.sha256(idempotency_key.encode()).hexdigest()[:12]}"
        self._idempotency_map[idempotency_key_map] = job_id
        self._jobs[(queue, idempotency_key)] = {"status": "pending", "payload": deepcopy(payload)}

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
        with self._lock:
            q = self._queues.get(queue)
            if q:
                envelope = q.popleft()
                job = self._jobs[(queue, envelope.idempotency_key)]
                if job["status"] == "pending":
                    job["status"] = "running"
                    envelope.delivery_id = str(uuid4())
                    job["delivery_id"] = envelope.delivery_id
                return envelope
            return None

    def ack(self, envelope: JobEnvelope) -> None:
        self.acknowledged.append(envelope.job_id)

    def nack(self, envelope: JobEnvelope, delay_s: int) -> None:
        self.nacked.append(envelope.job_id)
        envelope.retry_count += 1
        if envelope.queue not in self._queues:
            self._queues[envelope.queue] = deque()
        self._queues[envelope.queue].append(envelope)
