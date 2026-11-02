"""In-memory fake storage adapter for hermetic tests and development."""

import posixpath

from .port import StoragePort


class FakeStorageAdapter(StoragePort):
    """In-memory object storage adapter with local URL generation and traversal prevention."""

    def __init__(self, base_url: str = "http://localhost:8000/storage/fake"):
        self.base_url = base_url.rstrip("/")
        self._blobs: dict[str, bytes] = {}
        self._content_types: dict[str, str] = {}

    @staticmethod
    def normalize_key(key: str) -> str:
        """Sanitize and validate storage key to prevent path traversal."""
        clean = key.lstrip("/")
        norm = posixpath.normpath(clean)
        if norm == ".." or norm.startswith("../") or any(part == ".." for part in norm.split("/")):
            raise ValueError(f"Invalid storage key: path traversal detected in '{key}'")
        return norm

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
        return self.normalize_key(clean)

    def put(self, key: str, data: bytes, content_type: str) -> str:
        clean_key = self._resolve_key(key)
        self._blobs[clean_key] = data
        self._content_types[clean_key] = content_type
        return f"{self.base_url}/{clean_key}"

    def get(self, key: str) -> bytes:
        clean_key = self._resolve_key(key)
        if clean_key not in self._blobs:
            if key in self._blobs:
                return self._blobs[key]
            raise KeyError(f"Storage key not found: {key}")
        return self._blobs[clean_key]

    def get_content_type(self, key: str) -> str | None:
        """Return the stored content type for key if present."""
        clean_key = self._resolve_key(key)
        if clean_key in self._content_types:
            return self._content_types[clean_key]
        return self._content_types.get(key)

    def delete(self, key: str) -> None:
        clean_key = self._resolve_key(key)
        self._blobs.pop(clean_key, None)
        self._blobs.pop(key, None)
        self._content_types.pop(clean_key, None)
        self._content_types.pop(key, None)
