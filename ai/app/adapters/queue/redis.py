"""Redis queue adapter for asynchronous background job distribution."""

import hashlib
import json
import os
from typing import Any

from .port import JobEnvelope, QueuePort


class RedisQueueAdapter(QueuePort):
    """Redis background queue adapter implementing QueuePort.

    Supports an injected Redis client interface for hermetic testing without live Redis,
    or connects via REDIS_URL when running in production.
    """

    def __init__(self, redis_url: str | None = None, client: Any = None):
        self.acknowledged: list[str] = []
        self.nacked: list[str] = []

        if client is not None:
            self.client = client
            return

        url = redis_url or os.getenv("REDIS_URL")
        if not url:
            raise RuntimeError(
                "RedisQueueAdapter is not configured: REDIS_URL is not set. "
                "Use FakeQueueAdapter (default) via ADAPTER_QUEUE=fake or inject a Redis client."
            )

        try:
            import redis

            self.client = redis.from_url(url)
        except ImportError as err:
            raise RuntimeError(
                "redis package is not installed: install redis to use RedisQueueAdapter "
                "or inject a Redis client."
            ) from err

    def enqueue(self, queue: str, payload: dict[str, Any], idempotency_key: str) -> str:
        """Enqueue job with an idempotency key. Deduplicates duplicate submissions."""
        idemp_key = f"idemp:{queue}:{idempotency_key}"
        existing = self.client.get(idemp_key)
        if existing is not None:
            if isinstance(existing, bytes):
                return existing.decode("utf-8")
            return str(existing)

        job_id = f"job-redis-{hashlib.sha256(idempotency_key.encode()).hexdigest()[:12]}"
        envelope_data = {
            "job_id": job_id,
            "queue": queue,
            "payload": payload,
            "idempotency_key": idempotency_key,
            "retry_count": 0,
        }

        serialized = json.dumps(envelope_data)
        self.client.set(idemp_key, job_id)
        self.client.rpush(f"queue:{queue}", serialized)
        return job_id

    def dequeue(self, queue: str, timeout_s: int = 0) -> JobEnvelope | None:
        """Fetch next pending job envelope from queue, blocking up to timeout_s."""
        queue_key = f"queue:{queue}"
        raw: Any = None

        if timeout_s > 0 and hasattr(self.client, "blpop"):
            res = self.client.blpop([queue_key], timeout=timeout_s)
            if res is not None:
                # redis-py blpop returns tuple (b'queue:name', b'data')
                if isinstance(res, (tuple, list)) and len(res) == 2:
                    raw = res[1]
                else:
                    raw = res
        else:
            raw = self.client.lpop(queue_key)

        if raw is None:
            return None

        if isinstance(raw, bytes):
            raw = raw.decode("utf-8")

        try:
            data = json.loads(raw)
        except (json.JSONDecodeError, TypeError) as err:
            raise ValueError(f"Failed to deserialize job envelope: {raw}") from err

        return JobEnvelope(
            job_id=data["job_id"],
            queue=data["queue"],
            payload=data["payload"],
            idempotency_key=data["idempotency_key"],
            retry_count=data.get("retry_count", 0),
        )

    def ack(self, envelope: JobEnvelope) -> None:
        """Acknowledge completed job."""
        self.acknowledged.append(envelope.job_id)
        ack_key = f"queue:{envelope.queue}:ack"
        if hasattr(self.client, "sadd"):
            self.client.sadd(ack_key, envelope.job_id)

    def nack(self, envelope: JobEnvelope, delay_s: int = 0) -> None:
        """Reject and re-queue job after incrementing retry count."""
        self.nacked.append(envelope.job_id)
        envelope.retry_count += 1
        envelope_data = {
            "job_id": envelope.job_id,
            "queue": envelope.queue,
            "payload": envelope.payload,
            "idempotency_key": envelope.idempotency_key,
            "retry_count": envelope.retry_count,
        }
        serialized = json.dumps(envelope_data)
        self.client.rpush(f"queue:{envelope.queue}", serialized)
        nack_key = f"queue:{envelope.queue}:nack"
        if hasattr(self.client, "sadd"):
            self.client.sadd(nack_key, envelope.job_id)
