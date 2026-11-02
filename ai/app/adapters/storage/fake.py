"""In-memory fake storage adapter for hermetic tests and development."""

from .port import StoragePort


class FakeStorageAdapter(StoragePort):
    """In-memory object storage adapter with local URL generation."""

    def __init__(self, base_url: str = "http://localhost:8000/storage/fake"):
        self.base_url = base_url.rstrip("/")
        self._blobs: dict[str, bytes] = {}

    def put(self, key: str, data: bytes, content_type: str) -> str:
        clean_key = key.lstrip("/")
        self._blobs[clean_key] = data
        return f"{self.base_url}/{clean_key}"

    def get(self, key: str) -> bytes:
        clean_key = key.lstrip("/")
        if clean_key not in self._blobs:
            raise KeyError(f"Storage key not found: {key}")
        return self._blobs[clean_key]

    def delete(self, key: str) -> None:
        clean_key = key.lstrip("/")
        self._blobs.pop(clean_key, None)
