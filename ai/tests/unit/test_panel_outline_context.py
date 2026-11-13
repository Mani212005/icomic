"""The approved outline's events, cast and ending reach every panel-text call."""

import json

from app.adapters.language.fake import FakeLanguageAdapter
from app.services.panel import PanelService


class RecordingLanguage(FakeLanguageAdapter):
    def __init__(self):
        super().__init__()
        self.payloads = []

    def complete(self, model, messages, **kwargs):
        self.payloads.append(json.loads(messages[-1].content))
        return super().complete(model, messages, **kwargs)


def test_panel_generation_sends_the_approved_outline_detail_on_every_page():
    adapter = RecordingLanguage()
    script = " ".join(f"word{i}" for i in range(120))
    PanelService(adapter).generate_panel_text(
        1,
        "The bird",
        "A bird leads the way",
        ["Hero", "Bird", "Bystander"],
        panel_count=10,
        chapter_script=script,
        important_events=["Hero finds the bird", "The storm arrives early"],
        character_names=["Hero", "Bird"],
        ending="The door is locked",
    )
    assert len(adapter.payloads) == 2
    for payload in adapter.payloads:
        assert payload["operation"] == "panels"
        assert payload["outline_events"] == ["Hero finds the bird", "The storm arrives early"]
        assert payload["outline_characters"] == ["Hero", "Bird"]
        assert payload["outline_ending"] == "The door is locked"
        assert payload["characters"] == ["Hero", "Bird", "Bystander"]


def test_panel_generation_without_outline_detail_sends_empty_values():
    adapter = RecordingLanguage()
    PanelService(adapter).generate_panel_text(1, "T", "S", ["Hero"], panel_count=4)
    payload = adapter.payloads[0]
    assert payload["outline_events"] == []
    assert payload["outline_characters"] == []
    assert payload["outline_ending"] == ""
