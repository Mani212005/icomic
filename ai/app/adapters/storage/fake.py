"""In-memory fake storage adapter for hermetic tests and development."""

from .port import StoragePort


class FakeStorageAdapter(StoragePort):
    def __init__(self, base_url: str = "http://localhost:8000/storage/fake"):
        self.base_url = base_url
        self._blobs: dict[str, bytes] = {}

    def put(self, key: str, data: bytes, content_type: str) -> str:
        self._blobs[key] = data
        return f"{self.base_url}/{key}"

    def get(self, key: str) -> bytes:
        if key not in self._blobs:
            raise KeyError(f"Storage key not found: {key}")
        return self._blobs[key]

    def delete(self, key: str) -> None:
        self._blobs.pop(key, None)
