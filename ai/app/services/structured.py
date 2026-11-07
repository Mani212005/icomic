"""Bounded structured text calls. Invalid or incomplete output fails explicitly."""

import json

from jsonschema import Draft202012Validator, ValidationError

from ..adapters.language.port import LanguagePort, Message
from ..costs import GENERATION_POLICIES, STORY_INPUT_TOKEN_LIMIT
from ..provider_output import ProviderOutputError, reject_schema_echo

OVERLAY_PANEL_TEXT_POLICY = (
    "Write text for the actual A4 overlay layout: default lettering is 35 pt and may reduce "
    "to a documented 18 pt minimum. "
    "Each panel prefers an upper pocket at most 35 percent of the panel tall, and a caption may "
    "also sit on a panel corner. Keep each speech turn to at most 12 words, "
    "use at most two turns, and keep the single narration caption to at most 14 words. "
    "Begin every speech turn with the speaking character's exact name from the characters "
    "list and a colon, for example `Nia: Follow my light!`; the name is removed before "
    "lettering and used to point the tail at that speaker. Narration has no speaker. "
    "Shorten with care to preserve the story's essential event and intent; never omit an event "
    "or silently truncate text. The shaped-text fit check rejects a panel if its text cannot "
    "fit the actual A4 reserved zone at or above 18 pt. Do not narrate visible action or "
    "describe the picture."
)


def complete_json(
    adapter: LanguagePort,
    model: str,
    operation: str,
    payload: dict,
    schema: dict,
    *,
    overlay_lettering: bool = False,
):
    instruction = (
        OVERLAY_PANEL_TEXT_POLICY
        if operation == "panels" and overlay_lettering
        else "Write concise fields (at most 40 words each)."
    )
    messages = [
        Message(
            role="system",
            content=f"Return only JSON data for {operation}, not the schema itself. {instruction} Cover the entire source in order and return exactly the requested panel count. Follow this schema exactly: {json.dumps(schema)}",
        ),
        Message(role="user", content=json.dumps({"operation": operation, **payload})),
    ]
    # UTF-8 bytes conservatively bound tokenized input, including schema overhead.
    # Never trim a story or accept a partial response to fit the provider budget.
    response_format = {
        "type": "json_schema",
        "json_schema": {"name": operation, "schema": schema},
    }
    if (
        sum(len(m.content.encode()) for m in messages) + len(json.dumps(response_format).encode())
        > STORY_INPUT_TOKEN_LIMIT
    ):
        raise ValueError(
            "Structured request exceeds the bounded input budget; split the source further."
        )
    result = adapter.complete(
        model,
        messages,
        **GENERATION_POLICIES[{"characters": "analysis"}.get(operation, operation)].options(),
        response_format=response_format,
    )
    try:
        parsed = json.loads(result)
    except (TypeError, ValueError) as exc:
        raise ProviderOutputError(operation, "invalid_or_incomplete_json") from exc
    reject_schema_echo(parsed, operation)
    try:
        Draft202012Validator(schema).validate(parsed)
    except ValidationError as exc:
        if (
            overlay_lettering
            and operation == "panels"
            and exc.validator == "pattern"
            and exc.path
            and exc.path[-1] in {"dialogue", "narration"}
        ):
            raise ProviderOutputError(operation, "overlay_text_budget") from exc
        raise ProviderOutputError(operation, "schema_mismatch") from exc
    return parsed
