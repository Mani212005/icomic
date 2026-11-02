"""Pluggable policy seams for open captain decisions.

Open captain decisions:
1. User location and India restriction for image generation.
2. Image hosting architecture (public CDN vs presigned URLs).
3. 18+ age gate verification mechanism.

These are modeled as pluggable policy seams with safe defaults and documented TODOs.
Authentication and age verification logic belong to the Go backend per architecture.
"""

from typing import Protocol


# ============================================================================
# 1. Geo-restriction Policy Seam
# ============================================================================
# TODO(captain-decision): Geo-blocking policy for India and IP-based geo-detection.
# Open questions:
# - Is country determined via user profile self-declaration, IP-based geo-lookup, or both?
# - Is restriction enforced at the Go API boundary, Python worker, or edge proxy?
# Default: Permissive policy allowing generation, with seam ready for rule injection.
class GeoRestrictionPolicy(Protocol):
    """Protocol for evaluating whether image generation is permitted in a region."""

    def is_generation_allowed(
        self, country_code: str | None, hashed_user_id: str
    ) -> tuple[bool, str | None]:
        """Check if image generation is allowed for the user and country."""
        ...


class PermissiveGeoPolicy:
    """Safe default geo-policy allowing generation until captain decision is finalized."""

    def is_generation_allowed(
        self, country_code: str | None, hashed_user_id: str
    ) -> tuple[bool, str | None]:
        # Safe default: allow generation
        return True, None


class ConfigurableGeoPolicy:
    """Configurable geo-policy seam allowing restricted countries to be specified."""

    def __init__(self, restricted_countries: set[str] | None = None):
        self.restricted_countries = {c.upper() for c in (restricted_countries or set())}

    def is_generation_allowed(
        self, country_code: str | None, hashed_user_id: str
    ) -> tuple[bool, str | None]:
        if country_code and country_code.upper() in self.restricted_countries:
            return (
                False,
                f"Image generation is currently restricted in country: {country_code.upper()}.",
            )
        return True, None


# ============================================================================
# 2. Image Hosting Policy Seam
# ============================================================================
# TODO(captain-decision): Image hosting architecture.
# Open questions:
# - Will generated images be hosted on public S3, CDN (CloudFront), or behind Go presigned URLs?
# Default: DirectStoragePolicy passing through URLs from StoragePort.
class ImageHostingPolicy(Protocol):
    """Protocol for resolving storage keys and public access URLs."""

    def resolve_url(self, raw_url: str) -> str:
        """Transform or sign a storage URL for client consumption."""
        ...


class DirectStoragePolicy:
    """Safe default image hosting policy passing storage URLs through directly."""

    def resolve_url(self, raw_url: str) -> str:
        return raw_url


# ============================================================================
# 3. Age Gate Policy Seam
# ============================================================================
# TODO(captain-decision): 18+ age gate verification mechanism.
# Open questions:
# - DOB self-declaration vs credit card inference vs government ID check.
# Note: Per SPEC.md section 16 and ARCHITECTURE.md section 2, the Go API owns
# authentication, age gate, and session management before requests reach the Python
# AI service. The Python AI service does not implement age or auth logic.
class AgeGatePolicy(Protocol):
    """Protocol placeholder for age gate policy contract."""

    def is_verified_adult(self, hashed_user_id: str) -> bool:
        """Query if the user has satisfied the 18+ gate."""
        ...


class BackendTrustedAgePolicy:
    """Default policy: Python AI service trusts that Go backend has enforced the gate."""

    def is_verified_adult(self, hashed_user_id: str) -> bool:
        return True
