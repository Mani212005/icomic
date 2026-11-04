"""Bounded structured text calls. Invalid or incomplete output fails explicitly."""

import json

from ..adapters.language.port import LanguagePort, Message
from ..costs import STORY_INPUT_TOKEN_LIMIT, STORY_OUTPUT_TOKEN_LIMIT


def complete_json(adapter: LanguagePort, model: str, operation: str, payload: dict, schema: dict):
    messages = [
        Message(
            role="system",
            content=f"Return only valid JSON for {operation}. Write concise fields (at most 40 words each), cover the entire source in order, and return exactly the requested panel count. Follow this schema exactly: {json.dumps(schema)}",
        ),
        Message(role="user", content=json.dumps({"operation": operation, **payload})),
    ]
    # UTF-8 bytes conservatively bound tokenized input, including schema overhead.
    # Never trim a story or accept a partial response to fit the provider budget.
    if sum(len(m.content.encode()) for m in messages) > STORY_INPUT_TOKEN_LIMIT:
        raise ValueError(
            "Structured request exceeds the bounded input budget; split the source further."
        )
    result = adapter.complete(model, messages, max_tokens=STORY_OUTPUT_TOKEN_LIMIT)
    try:
        return json.loads(result)
    except (TypeError, ValueError) as exc:
        raise ValueError(f"Invalid or incomplete JSON from {operation}") from exc
