"""Adapter factory for configuration-selected dependency injection."""

import os
from dataclasses import dataclass

from .clock.fake import FakeClockAdapter
from .clock.port import ClockPort
from .clock.system import SystemClockAdapter
from .image.fake import FakeImageAdapter
from .image.meta import MetaImageAdapter
from .image.port import ImagePort
from .language.fake import FakeLanguageAdapter
from .language.meta import MetaLanguageAdapter
from .language.port import LanguagePort
from .queue.fake import FakeQueueAdapter
from .queue.port import QueuePort
from .queue.redis import RedisQueueAdapter
from .storage.fake import FakeStorageAdapter
from .storage.port import StoragePort
from .storage.s3 import S3Adapter
from .vision.fake import FakeVisionAdapter
from .vision.meta import MetaVisionAdapter
from .vision.port import VisionPort


@dataclass
class AdapterSet:
    """Container for wired adapter port implementations."""

    language: LanguagePort
    vision: VisionPort
    image: ImagePort
    storage: StoragePort
    queue: QueuePort
    clock: ClockPort


def get_language_adapter(selection: str | None = None) -> LanguagePort:
    name = (selection or os.getenv("ADAPTER_LANGUAGE", "fake")).strip().lower()
    if name == "fake":
        return FakeLanguageAdapter()
    if name == "meta":
        api_key = os.getenv("META_API_KEY")
        if not api_key or not api_key.strip():
            raise RuntimeError(
                "MetaLanguageAdapter is not configured: META_API_KEY environment variable is not set. "
                "Set META_API_KEY or use ADAPTER_LANGUAGE=fake."
            )
        return MetaLanguageAdapter(api_key=api_key)
    raise ValueError(f"Unknown language adapter: {name}. Supported: 'fake', 'meta'.")


def get_vision_adapter(selection: str | None = None) -> VisionPort:
    name = (selection or os.getenv("ADAPTER_VISION", "fake")).strip().lower()
    if name == "fake":
        return FakeVisionAdapter()
    if name == "meta":
        api_key = os.getenv("META_API_KEY")
        if not api_key or not api_key.strip():
            raise RuntimeError(
                "MetaVisionAdapter is not configured: META_API_KEY environment variable is not set. "
                "Set META_API_KEY or use ADAPTER_VISION=fake."
            )
        return MetaVisionAdapter(api_key=api_key)
    raise ValueError(f"Unknown vision adapter: {name}. Supported: 'fake', 'meta'.")


def get_image_adapter(selection: str | None = None) -> ImagePort:
    name = (selection or os.getenv("ADAPTER_IMAGE", "fake")).strip().lower()
    if name == "fake":
        return FakeImageAdapter()
    if name == "meta":
        api_key = os.getenv("META_API_KEY")
        if not api_key or not api_key.strip():
            raise RuntimeError(
                "MetaImageAdapter is not configured: META_API_KEY environment variable is not set. "
                "Set META_API_KEY or use ADAPTER_IMAGE=fake."
            )
        return MetaImageAdapter(api_key=api_key)
    raise ValueError(f"Unknown image adapter: {name}. Supported: 'fake', 'meta'.")


def get_storage_adapter(selection: str | None = None) -> StoragePort:
    name = (selection or os.getenv("ADAPTER_STORAGE", "fake")).strip().lower()
    if name == "fake":
        base_url = os.getenv("STORAGE_BASE_URL")
        return FakeStorageAdapter(base_url=base_url)
    if name == "s3":
        return S3Adapter()
    raise ValueError(f"Unknown storage adapter: {name}. Supported: 'fake', 's3'.")


def get_queue_adapter(selection: str | None = None) -> QueuePort:
    name = (selection or os.getenv("ADAPTER_QUEUE", "fake")).strip().lower()
    if name == "fake":
        return FakeQueueAdapter()
    if name == "redis":
        return RedisQueueAdapter()
    raise ValueError(f"Unknown queue adapter: {name}. Supported: 'fake', 'redis'.")


def get_clock_adapter(selection: str | None = None) -> ClockPort:
    name = (selection or os.getenv("ADAPTER_CLOCK", "fake")).strip().lower()
    if name == "fake":
        return FakeClockAdapter()
    if name == "system":
        return SystemClockAdapter()
    raise ValueError(f"Unknown clock adapter: {name}. Supported: 'fake', 'system'.")


def build_adapters_from_env() -> AdapterSet:
    """Build AdapterSet with configuration selected via ADAPTER_* env vars."""
    return AdapterSet(
        language=get_language_adapter(),
        vision=get_vision_adapter(),
        image=get_image_adapter(),
        storage=get_storage_adapter(),
        queue=get_queue_adapter(),
        clock=get_clock_adapter(),
    )
