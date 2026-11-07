"""Long stories preserve every source word and use bounded structured calls."""

import json
import math
from pathlib import Path

import pytest

from app.adapters.language.fake import FakeLanguageAdapter
from app.costs import STORY_INPUT_TOKEN_LIMIT, STORY_OUTPUT_TOKEN_LIMIT
from app.services.pacing import chapter_count, panel_count, split_text
from app.services.panel import PanelService
from app.services.script import ScriptService
from app.services.structured import OVERLAY_PANEL_TEXT_POLICY

FIXTURE = Path(__file__).resolve().parents[3] / "tests/fixtures/the_tide_clock.txt"


class RecordingLanguage(FakeLanguageAdapter):
    def __init__(self):
        super().__init__()
        self.calls = []

    def complete(self, model, messages, **kwargs):
        self.calls.append((messages, kwargs))
        return super().complete(model, messages, **kwargs)


@pytest.mark.parametrize(
    "words,expected", [(40, 4), (3000, 50), (3130, 51), (6000, 60), (12000, 120)]
)
def test_pacing_has_no_long_story_ceiling(words, expected):
    assert panel_count(words) == expected


def test_long_fixture_preserves_all_source_and_bounds_each_response():
    script = FIXTURE.read_text()
    adapter = RecordingLanguage()
    profiles, chapters, words, warnings = ScriptService(adapter).analyze_script(script)
    assert 3000 <= words <= 6000 and not warnings
    assert chapters == chapter_count(words) == 4
    outlines = PanelService(adapter).generate_chapter_outline(script, chapters, profiles)
    assert len(outlines) == 4
    all_panels = []
    total_pages = 0
    target = panel_count(words)
    for index, source in enumerate(split_text(script, chapters)):
        count = (index + 1) * target // chapters - index * target // chapters
        total_pages += math.ceil(count / 5)
        all_panels.extend(
            PanelService(adapter).generate_panel_text(
                index + 1,
                outlines[index].title,
                outlines[index].summary,
                [p.name for p in profiles],
                count,
                source,
            )
        )
    assert len(all_panels) == 51
    assert total_pages == 12
    assert " ".join(p.scene_description for p in all_panels).split() == script.split()
    required_image_text = (
        "no titles",
        "no headings",
        "no captions",
        "no page numbers",
        "no labels",
        "no notes",
        "no watermarks",
    )
    assert all(
        all(instruction in panel.image_prompt.lower() for instruction in required_image_text)
        for panel in all_panels
    )
    operations = [json.loads(messages[-1].content)["operation"] for messages, _ in adapter.calls]
    assert operations.count("characters") == 7
    assert operations.count("outline") == 8
    assert operations.count("panels") == 12
    for messages, kwargs in adapter.calls:
        assert sum(len(m.content.encode()) for m in messages) <= STORY_INPUT_TOKEN_LIMIT
        assert kwargs["max_tokens"] == STORY_OUTPUT_TOKEN_LIMIT


def test_overlay_panel_prompt_is_short_and_provider_text_limit_is_enforced():
    class LongDialogue(FakeLanguageAdapter):
        def complete(self, model, messages, **kwargs):
            self.messages = messages
            value = json.loads(super().complete(model, messages, **kwargs))
            value[0]["dialogue"] = (
                "This dialogue has more than twelve words and should be rejected by the parser now."
            )
            return json.dumps(value)

    adapter = LongDialogue()
    with pytest.raises(ValueError, match="12 words per speech turn"):
        PanelService(adapter).generate_panel_text(
            1, "Title", "Source", [], panel_count=1, overlay_lettering=True
        )
    system = adapter.messages[0].content
    expected = (Path(__file__).parents[1] / "fixtures/overlay-panel-text-policy.txt").read_text()
    assert OVERLAY_PANEL_TEXT_POLICY + "\n" == expected
    assert OVERLAY_PANEL_TEXT_POLICY in system


def test_overlay_panel_rejects_captions_over_fourteen_words():
    class LongCaption(FakeLanguageAdapter):
        def complete(self, model, messages, **kwargs):
            value = json.loads(super().complete(model, messages, **kwargs))
            value[0]["narration"] = (
                "This caption has more than fourteen words and should be rewritten while preserving the essential story meaning."
            )
            return json.dumps(value)

    with pytest.raises(ValueError, match="14 words per narration caption"):
        PanelService(LongCaption()).generate_panel_text(
            1, "Title", "Source", [], panel_count=1, overlay_lettering=True
        )


@pytest.mark.parametrize("response", ["[", "[]"])
def test_incomplete_panel_output_fails_instead_of_exporting_a_short_comic(response):
    class Incomplete(FakeLanguageAdapter):
        def complete(self, *args, **kwargs):
            return response

    with pytest.raises(ValueError):
        PanelService(Incomplete()).generate_panel_text(1, "Title", "Source", [])
