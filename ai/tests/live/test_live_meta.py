"""Optional live-API checks against real Meta endpoints (excluded from CI)."""

import os

import pytest

from app.adapters.image.meta import MetaImageAdapter


@pytest.mark.live
def test_live_meta_image_generation():
    api_key = os.getenv("META_API_KEY")
    if not api_key:
        pytest.skip("META_API_KEY is not set; skipping live check.")

    adapter = MetaImageAdapter(api_key=api_key)
    result = adapter.generate(
        prompt="A comic book hero in classic ink style",
        reference_images=[],
        hashed_user_id="test-hashed-user-id",
        style="comic",
    )
    assert result.image_bytes is not None
    assert "c2pa" in result.provenance_metadata
