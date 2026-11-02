"""In-memory fake storage adapter for hermetic tests and development."""

from .port import StoragePort


class FakeStorageAdapter(StoragePort):
    """In-memory object storage adapter with local URL generation."""

    def __init__(self, base_url: str = "http://localhost:8000/storage/fake"):
        self.base_url = base_url.rstrip("/")
        self._blobs: dict[str, bytes] = {}

    def _resolve_key(self, key: str) -> str:
        clean = key.strip()
        if clean.startswith(self.base_url):
            clean = clean[len(self.base_url) :]
        elif "://" in clean:
            parts = clean.split("/storage/fake/", 1)
            if len(parts) == 2:
                clean = parts[1]
            else:
                url_parts = clean.split("://", 1)[1].split("/", 1)
                if len(url_parts) == 2:
                    clean = url_parts[1]
        return clean.lstrip("/")

    def put(self, key: str, data: bytes, content_type: str) -> str:
        clean_key = key.lstrip("/")
        self._blobs[clean_key] = data
        return f"{self.base_url}/{clean_key}"

    def get(self, key: str) -> bytes:
        clean_key = self._resolve_key(key)
        if clean_key not in self._blobs:
            if key in self._blobs:
                return self._blobs[key]
            raise KeyError(f"Storage key not found: {key}")
        return self._blobs[clean_key]

    def delete(self, key: str) -> None:
        clean_key = self._resolve_key(key)
        self._blobs.pop(clean_key, None)
        self._blobs.pop(key, None)
