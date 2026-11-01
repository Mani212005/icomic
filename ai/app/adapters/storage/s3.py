"""S3-compatible object storage adapter."""

import os

from .port import StoragePort


class S3Adapter(StoragePort):
    def __init__(
        self,
        endpoint_url: str | None = None,
        bucket_name: str | None = None,
        access_key: str | None = None,
        secret_key: str | None = None,
    ):
        self.endpoint_url = endpoint_url or os.getenv("S3_ENDPOINT_URL")
        self.bucket_name = bucket_name or os.getenv("S3_BUCKET_NAME", "icomic-assets")
        self.access_key = access_key or os.getenv("AWS_ACCESS_KEY_ID")
        self.secret_key = secret_key or os.getenv("AWS_SECRET_ACCESS_KEY")

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
