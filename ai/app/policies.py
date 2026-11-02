"""Pluggable policy seams for image hosting architecture.

Owner decisions 2026-11-02:
- Users are global: no geo-blocking or region restrictions.
- 18+ age gate is deferred (advisory warning only; no enforced blocking).
- Pluggable image hosting architecture remains for future CDN/presigned URL integration.
"""

from typing import Protocol


# ============================================================================
# Image Hosting Policy Seam
# ============================================================================
class ImageHostingPolicy(Protocol):
    """Protocol for resolving storage keys and public access URLs."""

    def resolve_url(self, raw_url: str) -> str:
        """Transform or sign a storage URL for client consumption."""
        ...


class DirectStoragePolicy:
    """Safe default image hosting policy passing storage URLs through directly."""

    def resolve_url(self, raw_url: str) -> str:
        return raw_url
