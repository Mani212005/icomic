"""Queue port protocol for background job distribution."""

from dataclasses import dataclass
from typing import Any, Protocol, runtime_checkable


@dataclass
class JobEnvelope:
    job_id: str
    queue: str
    payload: dict[str, Any]
    idempotency_key: str
    retry_count: int = 0
    delivery_id: str = ""


@runtime_checkable
class QueuePort(Protocol):
    """Protocol defining asynchronous background job distribution."""

    synchronous: bool

    def get_job(self, queue: str, idempotency_key: str) -> dict[str, Any] | None:
        """Read saved request, status and terminal result, including after redelivery."""
        ...

    def finish(self, envelope: JobEnvelope, result: dict[str, Any]) -> None:
        """Save a terminal result before acknowledging delivery."""
        ...

    def enqueue(self, queue: str, payload: dict[str, Any], idempotency_key: str) -> str:
        """Enqueue job with an idempotency key. Returns job ID."""
        ...

    def dequeue(self, queue: str, timeout_s: int) -> JobEnvelope | None:
        """Fetch next pending job envelope from queue."""
        ...

    def ack(self, envelope: JobEnvelope) -> None:
        """Acknowledge completed job."""
        ...

    def nack(self, envelope: JobEnvelope, delay_s: int) -> None:
        """Reject and re-queue job after a delay."""
        ...
