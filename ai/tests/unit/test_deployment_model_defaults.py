"""Keep checked-in deployment defaults aligned with the Meta adapter defaults."""

import os
import re
from pathlib import Path

import pytest
import yaml

from app.adapters.meta_http import (
    DEFAULT_META_API_BASE_URL,
    DEFAULT_META_IMAGE_MODEL,
    DEFAULT_META_LANGUAGE_MODEL,
    DEFAULT_META_VISION_MODEL,
)

DEFAULTS = {
    "META_LANGUAGE_MODEL": DEFAULT_META_LANGUAGE_MODEL,
    "META_VISION_MODEL": DEFAULT_META_VISION_MODEL,
    "META_IMAGE_MODEL": DEFAULT_META_IMAGE_MODEL,
    "META_API_BASE_URL": DEFAULT_META_API_BASE_URL,
}
REPO_ROOT = Path(__file__).resolve().parents[3]


def assert_default(name, value):
    """Allow omission, adapter fallback, or the matching documented default."""
    if value is None or value == "":
        return
    value = str(value).strip().strip("\"'")
    match = re.fullmatch(r"\$\{" + name + r"(?::?-([^}]*))?\}", value)
    if match:
        value = match.group(1) or ""
    assert value in ("", DEFAULTS[name]), f"{name} deployment default differs from adapter"


def configuration_files():
    """Inspect templates only, never local credential files or dependency trees."""
    excluded = {".git", ".venv", "node_modules", "__pycache__", ".svelte-kit", "build"}
    for directory, children, names in os.walk(REPO_ROOT):
        children[:] = [
            name for name in children if name not in excluded and not name.startswith(".")
        ]
        for name in names:
            compose = "compose" in name and name.endswith((".yml", ".yaml"))
            template = "env" in name.lower() and name.endswith(
                (".example", ".sample", ".template", ".dist")
            )
            if compose or template:
                yield Path(directory) / name, compose


def test_deployment_model_and_endpoint_defaults():
    paths = list(configuration_files())
    assert any(path.name == "docker-compose.yml" for path, _ in paths)
    for path, compose in paths:
        text = path.read_text()
        if compose:
            services = yaml.safe_load(text).get("services", {})
            for service in services.values():
                environment = service.get("environment", {})
                if isinstance(environment, list):
                    environment = dict(
                        item.split("=", 1) if "=" in item else (item, None) for item in environment
                    )
                for name in DEFAULTS.keys() & environment.keys():
                    assert_default(name, environment[name])
        else:
            for line in text.splitlines():
                match = re.match(r"\s*(?:export\s+)?(META_\w+)\s*=\s*(.*?)\s*(?:#.*)?$", line)
                if match and match.group(1) in DEFAULTS:
                    assert_default(*match.groups())


@pytest.mark.parametrize("name", DEFAULTS)
def test_guard_accepts_adapter_fallbacks(name):
    for value in (
        None,
        "",
        DEFAULTS[name],
        "${" + name + "}",
        "${" + name + ":-" + DEFAULTS[name] + "}",
    ):
        assert_default(name, value)


@pytest.mark.parametrize(
    "retired",
    [
        "meta-llama-guard-3-8b",
        "meta-llama-3.3-70b-instruct",
        "meta-llama-3.2-11b-vision-instruct",
        "different-model",
    ],
)
@pytest.mark.parametrize("name", ["META_LANGUAGE_MODEL", "META_VISION_MODEL", "META_IMAGE_MODEL"])
def test_guard_rejects_retired_and_drifting_models(name, retired):
    for value in (retired, "${" + name + ":-" + retired + "}"):
        with pytest.raises(AssertionError, match="differs from adapter"):
            assert_default(name, value)


def test_guard_rejects_drifting_endpoint():
    with pytest.raises(AssertionError, match="differs from adapter"):
        assert_default("META_API_BASE_URL", "https://old-endpoint.invalid/v1")
