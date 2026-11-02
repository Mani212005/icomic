"""Storage port protocol for object storage operations."""

from typing import Protocol, runtime_checkable


@runtime_checkable
class StoragePort(Protocol):
    """Protocol defining object storage operations for images and PDFs."""

    def put(self, key: str, data: bytes, content_type: str) -> str:
        """Store blob and return access URL."""
        ...

    def get(self, key: str) -> bytes:
        """Retrieve stored blob."""
        ...

    def delete(self, key: str) -> None:
        """Delete stored blob."""
        ...
