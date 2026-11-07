"""Overlay story text is layout input only; legacy brief goldens stay unchanged."""

from app.gen.models import LetteringDocument
from app.services.render_brief import build_page_render_request
from tests.unit.test_render_brief import example_request


def test_overlay_brief_keeps_text_out_of_model_and_has_geometry():
    request = example_request()
    doc = LetteringDocument.model_validate(
        {
            "version": "lettering-v1",
            "page_id": "page",
            "art_mode": "text_free",
            "page_aspect": 2 / 3,
            "panels": [
                {
                    "panel_id": "p",
                    "order": 0,
                    "rect": {"x": 0.03, "y": 0.02, "width": 0.94, "height": 0.96},
                    "reserved_zones": [{"x": 0.04, "y": 0.04, "width": 0.92, "height": 0.34}],
                    "utterances": [
                        {
                            "utterance_id": "u",
                            "order": 0,
                            "kind": "speech",
                            "text": "SECRET LETTERING",
                            "speaker": "SECRET SPEAKER",
                        }
                    ],
                }
            ],
        }
    )
    prompt, _ = build_page_render_request(**request, lettering_document=doc)
    assert "page-render-brief-v6-overlay" in prompt
    assert "reserved_lettering_zones" in prompt and "page_geometry" in prompt
    assert "TEXT-FREE" in prompt and "no signs with readable text" in prompt
    assert "LARGE LOW-DETAIL EMPTY" in prompt
    assert "letters, words, numbers, signs, labels, symbols, writing" in prompt
    assert "glyph-like marks on any prop or scroll" in prompt
    assert "clear visual path from each pocket to its speaking character" in prompt
    assert "Draw exactly 1 separate bordered rectangular panels" in prompt
    assert "Never draw two copies of the same person" in prompt
    assert "draw exactly ONE bird" in prompt
    assert "SECRET" not in prompt
    assert request["panels"][0].dialogue not in prompt
    assert request["panels"][0].narration not in prompt
    doc.art_mode = "baked_in"
    baked, _ = build_page_render_request(**request, lettering_document=doc)
    legacy, _ = build_page_render_request(**request)
    assert baked == legacy
