"""Unit tests for pluggable policy seams representing open captain decisions."""

from app.policies import (
    BackendTrustedAgePolicy,
    ConfigurableGeoPolicy,
    DirectStoragePolicy,
    PermissiveGeoPolicy,
)


def test_permissive_geo_policy():
    policy = PermissiveGeoPolicy()
    allowed, reason = policy.is_generation_allowed(None, "user-hash-1")
    assert allowed is True
    assert reason is None

    allowed_in, reason_in = policy.is_generation_allowed("IN", "user-hash-1")
    assert allowed_in is True
    assert reason_in is None


def test_configurable_geo_policy():
    policy = ConfigurableGeoPolicy(restricted_countries={"IN"})
    allowed_us, reason_us = policy.is_generation_allowed("US", "user-hash-1")
    assert allowed_us is True
    assert reason_us is None

    allowed_in, reason_in = policy.is_generation_allowed("IN", "user-hash-1")
    assert allowed_in is False
    assert "restricted" in reason_in.lower()


def test_image_hosting_policy():
    policy = DirectStoragePolicy()
    url = "http://localhost:8000/storage/fake/pages/1/draft.png"
    assert policy.resolve_url(url) == url


def test_age_gate_policy():
    policy = BackendTrustedAgePolicy()
    assert policy.is_verified_adult("user-hash-1") is True
