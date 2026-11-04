"""Media fetch adapters and palette endpoint tests, with no live network access."""

import httpx
import pytest
from fastapi.testclient import TestClient

from app.adapters.clock.fake import FakeClockAdapter
from app.adapters.factory import AdapterSet, get_media_fetch_adapter
from app.adapters.image.fake import FakeImageAdapter
from app.adapters.language.fake import FakeLanguageAdapter
from app.adapters.media_fetch.fake import FakeMediaFetchAdapter
from app.adapters.media_fetch.http import HTTPMediaFetchAdapter
from app.adapters.queue.fake import FakeQueueAdapter
from app.adapters.storage.fake import FakeStorageAdapter
from app.adapters.vision.fake import FakeVisionAdapter
from app.main import create_app


class RecordingVisionAdapter(FakeVisionAdapter):
    def suggest_palette(self, model, image_bytes, panel_context, **kwargs):
        self.image_bytes = image_bytes
        self.panel_context = panel_context
        return super().suggest_palette(model, image_bytes, panel_context, **kwargs)


def test_palette_endpoint_passes_stored_image_bytes_and_context_to_vision():
    storage = FakeStorageAdapter("https://storage.example.test/assets")
    vision = RecordingVisionAdapter()
    app = create_app(
        AdapterSet(
            language=FakeLanguageAdapter(),
            vision=vision,
            image=FakeImageAdapter(),
            storage=storage,
            queue=FakeQueueAdapter(),
            clock=FakeClockAdapter(),
            media_fetch=FakeMediaFetchAdapter(storage),
        )
    )
    image_bytes = b"approved-line-art-png-data"
    image_url = storage.put("pages/approved.png", image_bytes, "image/png")
    response = TestClient(app).post(
        "/internal/v1/suggest-colour-palette",
        json={
            "job_id": "palette-job",
            "hashed_user_id": "user-hash",
            "project_id": "project-id",
            "page_id": "page-id",
            "line_art_image_url": image_url,
            "panel_context": "A rainy street with a red umbrella",
        },
    )
    assert response.status_code == 200
    assert vision.image_bytes == image_bytes
    assert vision.panel_context == "A rainy street with a red umbrella"
    assert response.json()["theme"]


def test_fake_media_fetch_only_reads_configured_storage():
    storage = FakeStorageAdapter("https://storage.example.test/assets")
    url = storage.put("pages/approved.png", b"image-bytes", "image/png")
    fetcher = FakeMediaFetchAdapter(storage)
    assert fetcher.fetch(url) == b"image-bytes"
    for invalid_url in (
        "https://other.example.test/assets/pages/approved.png",
        "https://storage.example.test/other/pages/approved.png",
        "https://storage.example.test/assets/pages/missing.png",
    ):
        with pytest.raises((ValueError, KeyError)):
            fetcher.fetch(invalid_url)


def make_http_fetcher(
    transport,
    *,
    storage_url="https://storage.example.test/assets",
    hosts=None,
    resolver=None,
    max_bytes=64,
):
    return HTTPMediaFetchAdapter(
        storage_url,
        hosts or {"images.example.test"},
        transport=transport,
        resolver=resolver or (lambda _host, _port: ["8.8.8.8"]),
        max_bytes=max_bytes,
    )


def test_http_media_fetch_allows_allowlisted_storage_and_external_hosts():
    seen = []

    def handler(request):
        seen.append(request.url.host)
        return httpx.Response(200, content=b"image-data")

    fetcher = make_http_fetcher(httpx.MockTransport(handler))
    assert fetcher.fetch("https://storage.example.test/assets/page.png") == b"image-data"
    assert fetcher.fetch("https://images.example.test/hosted/page.png") == b"image-data"
    assert seen == ["storage.example.test", "images.example.test"]


@pytest.mark.parametrize(
    "url",
    [
        "file:///etc/passwd",
        "http://images.example.test/page.png",
        "https://unlisted.example.test/page.png",
        "https://user:pass@images.example.test/page.png",
        "https://images.example.test/page.png#fragment",
        "https://images.example.test.evil.test/page.png",
        "https://storage.example.test/elsewhere/page.png",
        "https://storage.example.test/assets/%2e%2e/private.png",
    ],
)
def test_http_media_fetch_refuses_unsupported_and_unlisted_urls(url):
    fetcher = make_http_fetcher(httpx.MockTransport(lambda _request: httpx.Response(200)))
    with pytest.raises(ValueError, match="Media URL"):
        fetcher.fetch(url)


@pytest.mark.parametrize("host", ["127.0.0.1", "10.0.0.8", "169.254.169.254"])
def test_http_media_fetch_refuses_literal_private_loopback_and_link_local_ips(host):
    fetcher = make_http_fetcher(
        httpx.MockTransport(lambda _request: httpx.Response(200)), hosts={host}
    )
    with pytest.raises(ValueError, match="private, loopback, or link-local"):
        fetcher.fetch(f"https://{host}/image.png")


def test_http_media_fetch_refuses_dns_names_resolving_to_private_ips():
    fetcher = make_http_fetcher(
        httpx.MockTransport(lambda _request: httpx.Response(200)),
        resolver=lambda _host, _port: ["10.0.0.4"],
    )
    with pytest.raises(ValueError, match="private, loopback, or link-local"):
        fetcher.fetch("https://images.example.test/image.png")


def test_http_media_fetch_refuses_unresolvable_hosts():
    fetcher = make_http_fetcher(
        httpx.MockTransport(lambda _request: httpx.Response(200)),
        resolver=lambda _host, _port: [],
    )
    with pytest.raises(ValueError, match="no addresses"):
        fetcher.fetch("https://images.example.test/image.png")


def test_http_media_fetch_refuses_redirects_without_following_them():
    requests = []
    fetcher = make_http_fetcher(
        httpx.MockTransport(
            lambda request: (
                requests.append(request)
                or httpx.Response(302, headers={"location": "https://elsewhere.example/image.png"})
            )
        )
    )
    with pytest.raises(ValueError, match="redirects are not allowed"):
        fetcher.fetch("https://images.example.test/image.png")
    assert len(requests) == 1


@pytest.mark.parametrize("body,headers", [(b"x" * 65, {}), (b"small", {"content-length": "100"})])
def test_http_media_fetch_enforces_size_limit(body, headers):
    fetcher = make_http_fetcher(
        httpx.MockTransport(lambda _request: httpx.Response(200, content=body, headers=headers)),
        max_bytes=64,
    )
    with pytest.raises(ValueError, match="size limit"):
        fetcher.fetch("https://images.example.test/image.png")


def test_http_media_fetch_timeout_is_clear_and_does_not_retry():
    fetcher = make_http_fetcher(
        httpx.MockTransport(lambda _request: (_ for _ in ()).throw(httpx.ReadTimeout("slow")))
    )
    with pytest.raises(ValueError, match="timed out"):
        fetcher.fetch("https://images.example.test/image.png")


def test_http_media_fetch_refuses_empty_responses():
    fetcher = make_http_fetcher(
        httpx.MockTransport(lambda _request: httpx.Response(200, content=b""))
    )
    with pytest.raises(ValueError, match="response is empty"):
        fetcher.fetch("https://images.example.test/image.png")


def test_media_fetch_factory_defaults_to_fake_and_selects_http(monkeypatch):
    storage = FakeStorageAdapter("https://storage.example.test/assets")
    monkeypatch.delenv("ADAPTER_MEDIA_FETCH", raising=False)
    assert isinstance(get_media_fetch_adapter(storage), FakeMediaFetchAdapter)
    monkeypatch.setenv("STORAGE_BASE_URL", "https://storage.example.test/assets")
    monkeypatch.setenv("META_IMAGE_HOSTS", "images.example.test")
    assert isinstance(get_media_fetch_adapter(storage, "http"), HTTPMediaFetchAdapter)
