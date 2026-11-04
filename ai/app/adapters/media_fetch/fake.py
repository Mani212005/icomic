"""Hermetic media fetcher backed only by the configured in-memory storage."""

from urllib.parse import unquote, urlsplit

from ..storage.port import StoragePort
from .port import MediaFetchPort


class FakeMediaFetchAdapter(MediaFetchPort):
    """Resolve configured storage URLs through a storage port without network access."""

    def __init__(self, storage: StoragePort):
        self.storage = storage
        self.base_url = getattr(storage, "base_url", "").rstrip("/")
        if not self.base_url:
            raise ValueError("Fake media fetch requires storage with a configured base URL")
        parsed = urlsplit(self.base_url)
        self._origin = (parsed.scheme, parsed.netloc)
        self._path = parsed.path.rstrip("/")

    def fetch(self, url: str) -> bytes:
        parsed = urlsplit(url)
        if (parsed.scheme, parsed.netloc) != self._origin:
            raise ValueError("Media URL is not served by the configured storage base URL")
        if parsed.query or parsed.fragment or parsed.username or parsed.password:
            raise ValueError("Media URL contains unsupported URL components")
        decoded_path = unquote(parsed.path)
        if "\\" in decoded_path or any(part in {".", ".."} for part in decoded_path.split("/")):
            raise ValueError("Media URL contains an unsafe path")
        if not parsed.path.startswith(self._path + "/"):
            raise ValueError("Media URL is outside the configured storage base path")
        return self.storage.get(url)
