"""S3 object storage adapter stub."""

from .port import StoragePort


class S3Adapter(StoragePort):
    """S3-compatible object storage adapter stub.

    Deferred until live infrastructure deployment.
    """

    def __init__(self, *args, **kwargs):
        raise RuntimeError(
            "S3Adapter is not configured: live object storage integration is deferred "
            "until cloud deployment. Use FakeStorageAdapter (default) via ADAPTER_STORAGE=fake."
        )

    def put(self, key: str, data: bytes, content_type: str) -> str:
        raise NotImplementedError(
            "S3 live storage adapter deferred until real infrastructure deploy."
        )

    def get(self, key: str) -> bytes:
        raise NotImplementedError(
            "S3 live storage adapter deferred until real infrastructure deploy."
        )

    def delete(self, key: str) -> None:
        raise NotImplementedError(
            "S3 live storage adapter deferred until real infrastructure deploy."
        )
