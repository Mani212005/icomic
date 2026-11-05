"""Opt-in local storage keeps paid image bytes across adapter/process replacement."""

import hashlib

import pytest
from fastapi.testclient import TestClient

from app.adapters.factory import build_adapters_from_env, get_storage_adapter
from app.adapters.storage.fake import FakeStorageAdapter
from app.main import create_app
from tests.image_fixtures import image_bytes


def test_objects_and_types_survive_adapter_replacement_and_remain_independent(tmp_path):
    storage = FakeStorageAdapter(directory=tmp_path)
    original = image_bytes("approved draft")
    url = storage.put("pages/page-1/draft.webp", original, "image/webp")
    restored = FakeStorageAdapter(directory=tmp_path)
    assert restored.get(url) == original
    assert restored.get_content_type(url) == "image/webp"
    finished = image_bytes("new painted finish")
    second_url = restored.put("pages/page-1/new-finish.webp", finished, "image/webp")
    again = FakeStorageAdapter(directory=tmp_path)
    assert again.get(url) == original and again.get(second_url) == finished
    again.delete(second_url)
    assert FakeStorageAdapter(directory=tmp_path).get(url) == original
    with pytest.raises(KeyError):
        FakeStorageAdapter(directory=tmp_path).get(second_url)


def test_checkpoint_layout_serves_restored_source_bytes_through_http(tmp_path, monkeypatch):
    key = "characters/nia/reference.webp"
    original = image_bytes("Nia")
    path = tmp_path / "objects" / key
    path.parent.mkdir(parents=True)
    path.write_bytes(original)
    mime = tmp_path / "content-types" / (hashlib.sha256(key.encode()).hexdigest() + ".txt")
    mime.parent.mkdir()
    mime.write_text("image/webp")
    monkeypatch.setenv("ICOMIC_E2E_FAKE_STORAGE_DIR", str(tmp_path))
    monkeypatch.setenv("ICOMIC_E2E_RUN_ID", "a" * 32)
    for name in ("LANGUAGE", "IMAGE", "VISION", "STORAGE", "QUEUE", "CLOCK", "MEDIA_FETCH"):
        monkeypatch.setenv("ADAPTER_" + name, "fake")
    with TestClient(create_app(build_adapters_from_env())) as client:
        response = client.get("/storage/fake/" + key)
    assert response.status_code == 200
    assert response.content == original and response.headers["content-type"] == "image/webp"
    assert path.read_bytes() == original


def test_checkpoint_mode_requires_explicit_run_marker(tmp_path, monkeypatch):
    monkeypatch.setenv("ICOMIC_E2E_FAKE_STORAGE_DIR", str(tmp_path))
    monkeypatch.delenv("ICOMIC_E2E_RUN_ID", raising=False)
    with pytest.raises(ValueError, match="isolated E2E run ID"):
        get_storage_adapter("fake")


def test_checkpoint_refuses_traversal_and_symlink_escape(tmp_path):
    root = tmp_path / "checkpoint"
    outside = tmp_path / "outside"
    outside.mkdir()
    (root / "objects").mkdir(parents=True)
    (root / "objects" / "escape").symlink_to(outside, target_is_directory=True)
    storage = FakeStorageAdapter(directory=root)
    for key in ("../outside/secret", "escape/secret"):
        with pytest.raises(ValueError):
            storage.put(key, b"test", "text/plain")
    assert not list(outside.iterdir())


def test_default_storage_stays_in_memory():
    first = FakeStorageAdapter()
    url = first.put("same-key", b"one", "text/plain")
    with pytest.raises(KeyError):
        FakeStorageAdapter().get(url)
