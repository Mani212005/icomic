"""Fake object storage, in memory by default with an optional local checkpoint."""

import hashlib
import os
import posixpath
from pathlib import Path
from tempfile import NamedTemporaryFile

from .port import StoragePort

DEFAULT_STORAGE_BASE_URL = "http://localhost:8000/storage/fake"


class FakeStorageAdapter(StoragePort):
    """In-memory object storage adapter with local URL generation and traversal prevention."""

    def __init__(self, base_url: str | None = None, directory: Path | None = None):
        url = (
            base_url
            if base_url is not None
            else os.getenv("STORAGE_BASE_URL", DEFAULT_STORAGE_BASE_URL)
        )
        self.base_url = (url or DEFAULT_STORAGE_BASE_URL).rstrip("/")
        self._blobs: dict[str, bytes] = {}
        self._content_types: dict[str, str] = {}
        self.directory = Path(directory).resolve() if directory is not None else None

    def _disk_paths(self, key: str) -> tuple[Path, Path]:
        objects = self.directory / "objects"
        path = objects / key
        mime = (
            self.directory / "content-types" / (hashlib.sha256(key.encode()).hexdigest() + ".txt")
        )
        if any(
            not candidate.resolve().is_relative_to(self.directory) for candidate in (path, mime)
        ):
            raise ValueError("Storage key escapes the checkpoint directory")
        return path, mime

    @staticmethod
    def _write_atomic(path: Path, data: bytes) -> None:
        path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
        temporary = None
        try:
            with NamedTemporaryFile(dir=path.parent, delete=False) as output:
                temporary = Path(output.name)
                output.write(data)
                output.flush()
                os.fsync(output.fileno())
            os.replace(temporary, path)
        finally:
            if temporary is not None:
                temporary.unlink(missing_ok=True)

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
        if self.directory is not None:
            path, mime = self._disk_paths(clean_key)
            self._write_atomic(path, data)
            self._write_atomic(mime, content_type.encode())
        self._blobs[clean_key] = data
        self._content_types[clean_key] = content_type
        return f"{self.base_url}/{clean_key}"

    def get(self, key: str) -> bytes:
        clean_key = self._resolve_key(key)
        if clean_key not in self._blobs:
            if self.directory is not None:
                path, _ = self._disk_paths(clean_key)
                if path.is_file():
                    return path.read_bytes()
            if key in self._blobs:
                return self._blobs[key]
            raise KeyError(f"Storage key not found: {key}")
        return self._blobs[clean_key]

    def get_content_type(self, key: str) -> str | None:
        """Return the stored content type for key if present."""
        clean_key = self._resolve_key(key)
        if clean_key in self._content_types:
            return self._content_types[clean_key]
        if self.directory is not None:
            _, mime = self._disk_paths(clean_key)
            if mime.is_file():
                return mime.read_text()
        return self._content_types.get(key)

    def delete(self, key: str) -> None:
        clean_key = self._resolve_key(key)
        if self.directory is not None:
            for path in self._disk_paths(clean_key):
                path.unlink(missing_ok=True)
        self._blobs.pop(clean_key, None)
        self._blobs.pop(key, None)
        self._content_types.pop(clean_key, None)
        self._content_types.pop(key, None)
