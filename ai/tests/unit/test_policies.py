"""Unit tests for pluggable policy seams."""

from app.policies import DirectStoragePolicy


def test_image_hosting_policy():
    policy = DirectStoragePolicy()
    url = "http://localhost:8000/storage/fake/pages/1/draft.png"
    assert policy.resolve_url(url) == url
