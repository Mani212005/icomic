"""Queue consumer shared by inline fake delivery and the background Redis worker."""

import logging
from collections.abc import Callable
from threading import Event, Lock, Thread
from typing import Any

from fastapi import HTTPException
from pydantic import BaseModel

from ..adapters.queue.port import QueuePort

QUEUE = "generation"
logger = logging.getLogger(__name__)


class GenerationWorker:
    def __init__(self, queue: QueuePort):
        self.queue = queue
        self.handlers: dict[str, Callable[[dict], tuple[dict, float]]] = {}
        self.stop = Event()
        self._drain_lock = Lock()
        self._thread: Thread | None = None

    def register(self, operation: str, handler: Callable[[dict], tuple[dict, float]]) -> None:
        self.handlers[operation] = handler

    def run_once(self) -> bool:
        envelope = self.queue.dequeue(QUEUE, timeout_s=0)
        if envelope is None:
            return False
        saved = self.queue.get_job(QUEUE, envelope.idempotency_key)
        if saved and saved["status"] in ("done", "failed"):
            self.queue.ack(envelope)
            return True
        if saved and saved.get("delivery_id") != envelope.delivery_id:
            # A duplicate delivery cannot take another consumer's execution claim.
            self.queue.ack(envelope)
            return True
        try:
            result, cost = self.handlers[envelope.payload["operation"]](envelope.payload["request"])
        except Exception as exc:
            logger.exception("Generation consumer failed")
            self.queue.finish(
                envelope,
                {
                    "status": "failed",
                    "error_message": str(exc),
                    "http_status": 400 if isinstance(exc, ValueError) else 500,
                    "actual_cost": getattr(exc, "actual_cost", None),
                },
            )
        else:
            self.queue.finish(envelope, {"status": "done", "response": result, "actual_cost": cost})
        self.queue.ack(envelope)
        return True

    def submit(self, operation: str, request: BaseModel) -> dict[str, Any]:
        key = str(request.job_id)
        payload = {"operation": operation, "request": request.model_dump(mode="json")}
        self.queue.enqueue(QUEUE, payload, key)
        while True:
            if self.queue.synchronous:
                # Multiple HTTP threads may submit concurrently. One fake worker
                # drains at a time, preserving exactly one provider invocation.
                with self._drain_lock:
                    while self.run_once():
                        pass
            job = self.queue.get_job(QUEUE, key)
            if job is None:
                raise RuntimeError("Queued job disappeared")
            if job["payload"] != payload:
                raise HTTPException(409, "Idempotency key already belongs to another request")
            if job["status"] == "done":
                return job["response"]
            if job["status"] == "failed":
                raise HTTPException(job.get("http_status", 500), job["error_message"])
            if self.stop.wait(0.05):
                raise HTTPException(503, "Worker is stopping; retry with the same job ID")

    def start(self) -> None:
        if self.queue.synchronous:
            return

        def consume():
            while not self.stop.is_set():
                try:
                    worked = self.run_once()
                except Exception:
                    logger.exception("Queue delivery failed; awaiting durable recovery")
                    # Redis outages leave durable deliveries intact. Retry port
                    # access; lease expiry resolves uncertain provider outcomes.
                    worked = False
                if not worked:
                    self.stop.wait(0.1)

        self._thread = Thread(target=consume, name="generation-worker", daemon=True)
        self._thread.start()

    def close(self) -> None:
        self.stop.set()
        if self._thread:
            self._thread.join(timeout=5)
