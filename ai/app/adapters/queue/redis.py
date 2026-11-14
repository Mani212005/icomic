"""Durable Redis queue with atomic submission and saved idempotent results."""

import hashlib
import json
import os
import time
from dataclasses import asdict
from typing import Any
from uuid import uuid4

from .port import JobEnvelope, QueuePort

# All keys share a hash tag so these operations also work on Redis Cluster.
_ENQUEUE = """
if redis.call('EXISTS', KEYS[1]) == 0 then
    redis.call('SET', KEYS[1], ARGV[1])
    redis.call('RPUSH', KEYS[2], ARGV[2])
end
return 1
"""
_DEQUEUE = """
-- Expired deliveries have an unknown provider outcome. Never generate them twice.
local expired = redis.call('ZRANGEBYSCORE', KEYS[2], '-inf', ARGV[1], 'LIMIT', 0, 100)
for _, key in ipairs(expired) do
    local raw = redis.call('GET', key)
    if raw then
        local job = cjson.decode(raw)
        if job.status == 'running' then
            job.status = 'uncertain'
            job.error_message = 'Worker delivery expired; generation outcome is unknown.'
            job.http_status = 503
            redis.call('SET', key, cjson.encode(job))
        end
    end
    redis.call('ZREM', KEYS[2], key)
end
local raw = redis.call('LPOP', KEYS[1])
if not raw then return nil end
local envelope = cjson.decode(raw)
local key = ARGV[4] .. envelope.idempotency_key
local job = cjson.decode(redis.call('GET', key))
if job.status == 'pending' then
    job.status = 'running'
    job.delivery_id = ARGV[3]
    redis.call('SET', key, cjson.encode(job))
    redis.call('ZADD', KEYS[2], ARGV[2], key)
end
envelope.delivery_id = ARGV[3]
return cjson.encode(envelope)
"""
_FINISH = """
local job = cjson.decode(redis.call('GET', KEYS[1]))
if job.status == 'running' and job.delivery_id == ARGV[1] then
    local result = cjson.decode(ARGV[2])
    for k, v in pairs(result) do job[k] = v end
    redis.call('SET', KEYS[1], cjson.encode(job))
    redis.call('ZREM', KEYS[2], KEYS[1])
    return 1
end
return 0
"""
_NACK = """
local job = cjson.decode(redis.call('GET', KEYS[1]))
if job.status == 'running' and job.delivery_id == ARGV[1] then
    job.status = 'pending'
    redis.call('SET', KEYS[1], cjson.encode(job))
    redis.call('ZREM', KEYS[2], KEYS[1])
    redis.call('RPUSH', KEYS[3], ARGV[2])
end
return 1
"""


class RedisQueueAdapter(QueuePort):
    """Redis state survives service restarts; clients are injectable for hermetic tests.

    The delivery lease bounds abandoned running jobs. An expired delivery becomes
    uncertain rather than repeating a possibly completed, billed provider operation.
    Completed results are saved before ack, so an ack failure/redelivery never
    generates again.
    """

    synchronous = False

    def __init__(self, redis_url: str | None = None, client: Any = None, lease_s: int = 900):
        self.acknowledged: list[str] = []
        self.nacked: list[str] = []
        self.lease_s = lease_s
        if client is not None:
            self.client = client
            return
        url = redis_url or os.getenv("REDIS_URL")
        if not url:
            raise RuntimeError("RedisQueueAdapter is not configured: REDIS_URL is not set.")
        import redis

        self.client = redis.from_url(url)

    def ready(self) -> None:
        """Raise unless Redis answers, so readiness reflects the real queue."""
        self.client.ping()

    def _prefix(self, queue: str) -> str:
        return f"icomic:{{{queue}}}:"

    def get_job(self, queue: str, idempotency_key: str) -> dict[str, Any] | None:
        raw = self.client.get(self._prefix(queue) + "job:" + idempotency_key)
        if raw is None:
            return None
        job = json.loads(raw)
        job["payload"] = json.loads(job.pop("payload_json"))
        if "response_json" in job:
            job["response"] = json.loads(job.pop("response_json"))
        return job

    def enqueue(self, queue: str, payload: dict[str, Any], idempotency_key: str) -> str:
        job_id = f"job-redis-{hashlib.sha256(idempotency_key.encode()).hexdigest()[:12]}"
        envelope = asdict(JobEnvelope(job_id, queue, payload, idempotency_key))
        envelope["payload"] = json.dumps(payload)
        prefix = self._prefix(queue)
        self.client.eval(
            _ENQUEUE,
            2,
            prefix + "job:" + idempotency_key,
            prefix + "ready",
            json.dumps({"status": "pending", "payload_json": json.dumps(payload)}),
            json.dumps(envelope),
        )
        return job_id

    def dequeue(self, queue: str, timeout_s: int = 0) -> JobEnvelope | None:
        prefix = self._prefix(queue)
        deadline = time.monotonic() + timeout_s
        while True:
            now = time.time()
            raw = self.client.eval(
                _DEQUEUE,
                2,
                prefix + "ready",
                prefix + "running",
                now,
                now + self.lease_s,
                str(uuid4()),
                prefix + "job:",
            )
            if raw is not None:
                data = json.loads(raw)
                data["payload"] = json.loads(data["payload"])
                return JobEnvelope(**data)
            if time.monotonic() >= deadline:
                return None
            time.sleep(0.05)

    def finish(self, envelope: JobEnvelope, result: dict[str, Any]) -> None:
        prefix = self._prefix(envelope.queue)
        result = dict(result)
        if "response" in result:
            result["response_json"] = json.dumps(result.pop("response"))
        saved = self.client.eval(
            _FINISH,
            2,
            prefix + "job:" + envelope.idempotency_key,
            prefix + "running",
            envelope.delivery_id,
            json.dumps(result),
        )
        if not saved:
            raise RuntimeError("Delivery no longer owns the running job")

    def ack(self, envelope: JobEnvelope) -> None:
        # finish already durably saved the result and released the delivery.
        self.acknowledged.append(envelope.job_id)

    def nack(self, envelope: JobEnvelope, delay_s: int = 0) -> None:
        """Retry only before invoking the provider; never nack an uncertain outcome."""
        self.nacked.append(envelope.job_id)
        envelope.retry_count += 1
        prefix = self._prefix(envelope.queue)
        data = asdict(envelope)
        data["payload"] = json.dumps(envelope.payload)
        self.client.eval(
            _NACK,
            3,
            prefix + "job:" + envelope.idempotency_key,
            prefix + "running",
            prefix + "ready",
            envelope.delivery_id,
            json.dumps(data),
        )
