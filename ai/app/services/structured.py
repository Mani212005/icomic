"""Bounded structured text calls. Invalid or incomplete output fails explicitly."""

import json

from jsonschema import Draft202012Validator, ValidationError

from ..adapters.language.port import LanguagePort, Message
from ..costs import GENERATION_POLICIES, STORY_INPUT_TOKEN_LIMIT
from ..provider_output import ProviderOutputError, reject_schema_echo


def complete_json(adapter: LanguagePort, model: str, operation: str, payload: dict, schema: dict):
    messages = [
        Message(
            role="system",
            content=f"Return only JSON data for {operation}, not the schema itself. Write concise fields (at most 40 words each), cover the entire source in order, and return exactly the requested panel count. Follow this schema exactly: {json.dumps(schema)}",
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
        raise ProviderOutputError(operation, "schema_mismatch") from exc
    return parsed
