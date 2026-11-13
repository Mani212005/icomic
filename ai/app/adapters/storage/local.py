"""Local-disk object storage for a personal run: every object lives on a mounted volume."""

import os
from pathlib import Path

from .fake import FakeStorageAdapter


class LocalStorageAdapter(FakeStorageAdapter):
    """Durable storage rooted at one directory, with no in-memory copy of any object.

    The on-disk layout (``objects/<key>`` plus ``content-types/<sha256>.txt``) and the
    public URL shape (``<STORAGE_BASE_URL>/<key>``) match the checkpoint mode of the fake
    adapter, so URLs already saved in the database keep resolving and a restart, rebuild
    or container replacement loses nothing as long as the directory is a volume.
    Every read goes to disk, so two processes sharing the directory see the same objects.
    """

    def __init__(self, directory: Path | str, base_url: str | None = None):
        if directory is None or not str(directory).strip():
            raise ValueError("Local storage requires a directory")
        super().__init__(base_url=base_url, directory=Path(directory))
        for name in ("objects", "content-types"):
            (self.directory / name).mkdir(parents=True, exist_ok=True, mode=0o700)
        if not os.access(self.directory, os.W_OK | os.X_OK):
            raise PermissionError(f"Local storage directory is not writable: {self.directory}")

    def ready(self) -> None:
        """Raise unless the volume is still mounted and writable."""
        if not os.access(self.directory / "objects", os.W_OK | os.X_OK):
            raise PermissionError("Local storage directory is not writable")

    def put(self, key: str, data: bytes, content_type: str) -> str:
        clean_key = self._resolve_key(key)
        path, mime = self._disk_paths(clean_key)
        self._write_atomic(path, data)
        self._write_atomic(mime, content_type.encode())
        return f"{self.base_url}/{clean_key}"

    def get(self, key: str) -> bytes:
        path, _ = self._disk_paths(self._resolve_key(key))
        if not path.is_file():
            raise KeyError(f"Storage key not found: {key}")
        return path.read_bytes()

    def get_content_type(self, key: str) -> str | None:
        _, mime = self._disk_paths(self._resolve_key(key))
        return mime.read_text() if mime.is_file() else None

    def delete(self, key: str) -> None:
        for path in self._disk_paths(self._resolve_key(key)):
            path.unlink(missing_ok=True)
