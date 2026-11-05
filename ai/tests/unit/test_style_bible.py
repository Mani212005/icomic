"""Unit tests and golden verification for the style bible resolver."""

import json
from pathlib import Path

import pytest

from app.services.style_bible import (
    STYLE_BIBLE_VERSION,
    STYLE_PRESETS,
    resolve_style_bible,
)


@pytest.mark.parametrize("preset_id", list(STYLE_PRESETS.keys()))
def test_preset_matches_checked_in_golden(preset_id: str):
    """Every style preset must match its checked-in golden fixture exactly."""
    preset = resolve_style_bible(preset_id)
    golden_path = Path(__file__).parents[1] / "fixtures" / "style_presets" / f"{preset_id}.json"
    assert golden_path.exists(), f"Golden file missing: {golden_path}"
    golden_data = json.loads(golden_path.read_text(encoding="utf-8"))

    actual_data = preset.to_dict()
    assert actual_data == golden_data
    assert preset.version == STYLE_BIBLE_VERSION
    assert preset.bible_hash == golden_data["bible_hash"]


def test_preset_hashes_are_unique_and_deterministic():
    """Each preset has a unique, deterministic SHA-256 hash."""
    hashes = {preset.style_id: preset.bible_hash for preset in STYLE_PRESETS.values()}
    assert len(hashes) == len(STYLE_PRESETS)
    for style_id, h in hashes.items():
        assert len(h) == 64
        # Re-resolve and verify stability
        assert resolve_style_bible(style_id).bible_hash == h


def test_unknown_style_fallback_is_safe_and_deterministic():
    """Unknown or custom styles fall back to a coherent, deterministic StyleBible."""
    bible1 = resolve_style_bible("custom-retro-noir-99")
    bible2 = resolve_style_bible("custom-retro-noir-99")

    assert bible1.style_id == "custom-retro-noir-99"
    assert bible1.label == "Custom Retro Noir 99"
    assert bible1.version == STYLE_BIBLE_VERSION
    assert bible1.bible_hash == bible2.bible_hash
    assert bible1.line_treatment
    assert bible1.environment
    assert bible1.materials
    assert bible1.values
    assert bible1.palette.theme
    assert len(bible1.palette.dominant_tones) > 0
    assert len(bible1.palette.accent_tones) > 0
    assert bible1.lighting
    assert bible1.mood
    assert bible1.depth
    assert len(bible1.negative_constraints) > 0


def test_default_fallback_when_style_is_none_or_empty():
    """None or empty string resolves to classic-comic default."""
    assert resolve_style_bible(None).style_id == "classic-comic"
    assert resolve_style_bible("").style_id == "classic-comic"
    assert resolve_style_bible("   ").style_id == "classic-comic"


@pytest.mark.parametrize("preset_id", list(STYLE_PRESETS.keys()))
def test_stage_prompts_contain_no_contradictory_exclusions(preset_id: str):
    """Line art stage prompt must not contradict style with blanket 'flat white fills, no shading'."""
    preset = resolve_style_bible(preset_id)
    line_art_prompt = preset.format_for_line_art(page_number=1, panel_count=4)
    assert "no shading" not in line_art_prompt.lower()
    assert "flat white fills" not in line_art_prompt.lower()
    assert preset.line_treatment in line_art_prompt
    assert preset.values in line_art_prompt


def test_palette_vision_enrichment():
    """Vision palette format enriches context with theme and color tones."""
    preset = resolve_style_bible("cyberpunk-noir")
    enriched = preset.format_for_palette_vision("Page 2 of chapter 1")
    assert "Page 2 of chapter 1" in enriched
    assert preset.palette.theme in enriched
    assert "hot magenta" in enriched
    assert "#00F0FF" in enriched
