"""Allowlisted HTTP media fetcher with bounded response handling."""

from __future__ import annotations

import ipaddress
import socket
from collections.abc import Callable
from urllib.parse import unquote, urlsplit

import httpx

from .port import MediaFetchPort


class HTTPMediaFetchAdapter(MediaFetchPort):
    """Fetch allowlisted images without redirects, private addresses, or unbounded reads."""

    def __init__(
        self,
        storage_base_url: str,
        meta_image_hosts: set[str] | None = None,
        *,
        transport: httpx.BaseTransport | None = None,
        resolver: Callable[[str, int], list[str]] | None = None,
        timeout_seconds: float = 5.0,
        max_bytes: int = 10 * 1024 * 1024,
    ):
        self.storage = urlsplit(storage_base_url.rstrip("/"))
        if self.storage.scheme not in {"http", "https"} or not self.storage.hostname:
            raise ValueError("STORAGE_BASE_URL must be an absolute HTTP URL")
        self.storage_path = self.storage.path.rstrip("/")
        self.meta_image_hosts = {
            host.strip().lower().rstrip(".") for host in (meta_image_hosts or set())
        }
        self.transport = transport
        self.resolver = resolver or self._resolve_addresses
        self.timeout_seconds = timeout_seconds
        self.max_bytes = max_bytes

    @staticmethod
    def _resolve_addresses(host: str, port: int) -> list[str]:
        return list(
            dict.fromkeys(
                result[4][0] for result in socket.getaddrinfo(host, port, type=socket.SOCK_STREAM)
            )
        )

    def _check_address_safety(self, host: str, port: int) -> None:
        try:
            addresses = [str(ipaddress.ip_address(host))]
        except ValueError:
            try:
                addresses = self.resolver(host, port)
            except (OSError, ValueError) as exc:
                raise ValueError("Media URL host could not be resolved safely") from exc
        if not addresses:
            raise ValueError("Media URL host has no addresses")
        for address in addresses:
            try:
                parsed = ipaddress.ip_address(address)
            except ValueError as exc:
                raise ValueError("Media URL resolved to an invalid address") from exc
            if not parsed.is_global:
                raise ValueError("Media URL resolves to a private, loopback, or link-local address")

    def _validate_url(self, url: str) -> tuple[str, int]:
        parsed = urlsplit(url)
        if parsed.scheme not in {"http", "https"} or not parsed.hostname:
            raise ValueError("Media URL must be an absolute HTTP or HTTPS URL")
        if parsed.username or parsed.password or parsed.fragment:
            raise ValueError("Media URL contains unsupported URL components")
        decoded_path = unquote(parsed.path)
        if "\\" in decoded_path or any(part in {".", ".."} for part in decoded_path.split("/")):
            raise ValueError("Media URL contains an unsafe path")
        host = parsed.hostname.lower().rstrip(".")
        port = parsed.port or (443 if parsed.scheme == "https" else 80)
        storage_host = (self.storage.hostname or "").lower().rstrip(".")
        is_storage = (
            host == storage_host
            and parsed.scheme == self.storage.scheme
            and port == (self.storage.port or (443 if self.storage.scheme == "https" else 80))
            and parsed.path.startswith(self.storage_path + "/")
        )
        is_external = host in self.meta_image_hosts and parsed.scheme == "https" and port == 443
        if not (is_storage or is_external):
            raise ValueError("Media URL host or scheme is not allowlisted")
        self._check_address_safety(host, port)
        return host, port

    def fetch(self, url: str) -> bytes:
        self._validate_url(url)
        timeout = httpx.Timeout(self.timeout_seconds)
        try:
            with (
                httpx.Client(
                    transport=self.transport,
                    timeout=timeout,
                    follow_redirects=False,
                    trust_env=False,
                ) as client,
                client.stream("GET", url) as response,
            ):
                if 300 <= response.status_code < 400:
                    raise ValueError("Media URL redirects are not allowed")
                response.raise_for_status()
                length = response.headers.get("content-length")
                if length and int(length) > self.max_bytes:
                    raise ValueError("Media response exceeds the configured size limit")
                chunks: list[bytes] = []
                total = 0
                for chunk in response.iter_bytes():
                    total += len(chunk)
                    if total > self.max_bytes:
                        raise ValueError("Media response exceeds the configured size limit")
                    chunks.append(chunk)
                if not total:
                    raise ValueError("Media response is empty")
                return b"".join(chunks)
        except httpx.TimeoutException as exc:
            raise ValueError("Media fetch timed out") from exc
        except httpx.HTTPError as exc:
            raise ValueError(f"Media fetch failed: {exc}") from exc
