"""Adapter factory for configuration-selected dependency injection."""

import os
import re
from dataclasses import dataclass
from pathlib import Path

from ..provider_costs import ProviderCostLedger
from ..provider_diagnostics import ProviderDiagnostics
from .clock.fake import FakeClockAdapter
from .clock.port import ClockPort
from .clock.system import SystemClockAdapter
from .image.fake import FakeImageAdapter
from .image.meta import MetaImageAdapter
from .image.port import ImagePort
from .language.fake import FakeLanguageAdapter
from .language.meta import MetaLanguageAdapter
from .language.port import LanguagePort
from .media_fetch.fake import FakeMediaFetchAdapter
from .media_fetch.http import HTTPMediaFetchAdapter
from .media_fetch.port import MediaFetchPort
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
    media_fetch: MediaFetchPort | None = None
    cost_ledger: ProviderCostLedger | None = None
    diagnostics: ProviderDiagnostics | None = None


def get_media_fetch_adapter(storage: StoragePort, selection: str | None = None) -> MediaFetchPort:
    name = (selection or os.getenv("ADAPTER_MEDIA_FETCH", "fake")).strip().lower()
    if name == "fake":
        return FakeMediaFetchAdapter(storage)
    if name == "http":
        storage_base_url = os.getenv("STORAGE_BASE_URL", "")
        if not storage_base_url:
            raise RuntimeError("HTTP media fetch requires STORAGE_BASE_URL")
        meta_hosts = {
            host.strip() for host in os.getenv("META_IMAGE_HOSTS", "").split(",") if host.strip()
        }
        return HTTPMediaFetchAdapter(storage_base_url, meta_hosts)
    raise ValueError(f"Unknown media fetch adapter: {name}. Supported: 'fake', 'http'.")


def get_language_adapter(
    selection: str | None = None, cost_ledger=None, diagnostics=None
) -> LanguagePort:
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
        return MetaLanguageAdapter(
            api_key=api_key, cost_ledger=cost_ledger, cost_stage="language", diagnostics=diagnostics
        )
    raise ValueError(f"Unknown language adapter: {name}. Supported: 'fake', 'meta'.")


def get_vision_adapter(
    selection: str | None = None, cost_ledger=None, diagnostics=None
) -> VisionPort:
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
        return MetaVisionAdapter(
            api_key=api_key, cost_ledger=cost_ledger, cost_stage="vision", diagnostics=diagnostics
        )
    raise ValueError(f"Unknown vision adapter: {name}. Supported: 'fake', 'meta'.")


def get_image_adapter(
    selection: str | None = None, cost_ledger=None, diagnostics=None
) -> ImagePort:
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
        return MetaImageAdapter(
            api_key=api_key, cost_ledger=cost_ledger, cost_stage="image", diagnostics=diagnostics
        )
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
    storage = get_storage_adapter()
    diagnostics_path = os.getenv("ICOMIC_E2E_DIAGNOSTICS_DIR")
    diagnostics = ProviderDiagnostics(Path(diagnostics_path)) if diagnostics_path else None
    cost_ledger = None
    if os.getenv("ICOMIC_E2E_LIVE") == "1":
        run_id = os.getenv("ICOMIC_E2E_RUN_ID", "")
        if not re.fullmatch(r"[0-9a-f]{32}", run_id):
            raise ValueError("Capped E2E requires a unique ICOMIC_E2E_RUN_ID")
        cost_ledger = ProviderCostLedger(
            cap=os.getenv("ICOMIC_E2E_COST_CAP_USD", "0"),
            prior=os.getenv("ICOMIC_E2E_PRIOR_SPEND_USD", "0"),
            reserve=os.getenv("ICOMIC_E2E_THIRD_CHECK_RESERVE_USD", "0"),
            path=Path("/var/lib/icomic-e2e") / f"provider-costs-{run_id}.json",
            run_id=run_id,
        )
    return AdapterSet(
        language=get_language_adapter(cost_ledger=cost_ledger, diagnostics=diagnostics),
        vision=get_vision_adapter(cost_ledger=cost_ledger, diagnostics=diagnostics),
        image=get_image_adapter(cost_ledger=cost_ledger, diagnostics=diagnostics),
        storage=storage,
        queue=get_queue_adapter(),
        clock=get_clock_adapter(),
        media_fetch=get_media_fetch_adapter(storage),
        cost_ledger=cost_ledger,
        diagnostics=diagnostics,
    )
