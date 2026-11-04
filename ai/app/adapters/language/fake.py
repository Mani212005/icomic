"""Deterministic fake language adapter for hermetic testing and development."""

import hashlib
import json

from .port import LanguagePort, Message, TokenUsage


class FakeLanguageAdapter(LanguagePort):
    """Deterministic in-memory language model adapter with no external calls."""

    def __init__(
        self,
        default_response: str = "Deterministic test response",
        replay_adapter: LanguagePort | None = None,
    ):
        self.default_response = default_response
        self.replay_adapter = replay_adapter
        self.last_usage = None

    def complete(self, model: str, messages: list[Message], **kwargs) -> str:
        if self.replay_adapter is not None:
            if hasattr(self.replay_adapter, "last_usage"):
                self.replay_adapter.last_usage = None
            try:
                return self.replay_adapter.complete(model, messages, **kwargs)
            finally:
                self.last_usage = getattr(self.replay_adapter, "last_usage", None)
        if messages and messages[0].content.startswith("Evaluate prompt for safety"):
            return "SAFE"
        try:
            payload = json.loads(messages[-1].content)
        except (ValueError, IndexError):
            payload = {}
        source = payload.get("source", "")
        operation = payload.get("operation")
        if operation == "characters":
            return json.dumps(
                [
                    {
                        "name": "Protagonist",
                        "description": "The central figure described in the script",
                        "age_range": "20-30",
                        "appearance": "Determined expression, medium build",
                        "hair_style": "Dark wavy hair",
                        "clothing": "Work coat and boots",
                        "personality": "Observant and persistent",
                        "visual_attributes": {},
                    }
                ]
            )
        if operation == "outline":
            number = payload["chapter_number"]
            return json.dumps(
                {
                    "chapter_number": number,
                    "title": f"Chapter {number}",
                    "summary": " ".join(source.split()[:40]),
                    "important_events": [" ".join(source.split()[-20:])],
                    "character_names": payload["characters"],
                    "ending": "Continue to the next story beat.",
                }
            )
        if operation == "panels":
            words = source.split()
            count = payload["panel_count"]
            return json.dumps(
                [
                    {
                        "panel_number": i + 1,
                        "scene_description": " ".join(
                            words[i * len(words) // count : (i + 1) * len(words) // count]
                        )
                        or "A quiet transition.",
                        "character_actions": "The character follows the story action.",
                        "camera_angle": "Eye level",
                        "background": "The setting described in the scene.",
                        "dialogue": "",
                        "narration": "",
                        "image_prompt": "Comic scene: "
                        + " ".join(words[i * len(words) // count : (i + 1) * len(words) // count]),
                    }
                    for i in range(count)
                ]
            )
        prompt_concat = "".join(f"{m.role}:{m.content}\n" for m in messages)
        h = hashlib.sha256(prompt_concat.encode("utf-8")).hexdigest()[:8]
        return f"[FakeLanguageAdapter hash={h}] {self.default_response}"

    def estimate_tokens(self, messages: list[Message]) -> TokenUsage:
        chars = sum(len(m.content) for m in messages)
        est_tokens = max(1, chars // 4)
        return TokenUsage(input_tokens=est_tokens, output_tokens=50, total_tokens=est_tokens + 50)
