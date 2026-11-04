"""Port for resolving approved image URLs into image bytes."""

from typing import Protocol, runtime_checkable


@runtime_checkable
class MediaFetchPort(Protocol):
    """Fetch image bytes for an approved media URL."""

    def fetch(self, url: str) -> bytes: ...
