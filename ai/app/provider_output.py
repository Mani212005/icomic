"""Typed, non-retryable rejection of malformed model output."""


class ProviderOutputError(ValueError):
    def __init__(self, operation: str, kind: str):
        self.operation = operation
        self.kind = kind
        super().__init__(f"Invalid provider output for {operation}: {kind}")


def reject_schema_echo(value, operation: str) -> None:
    if isinstance(value, dict) and (
        "$defs" in value
        or "$schema" in value
        or (
            value.get("type") in ("object", "array")
            and any(k in value for k in ("properties", "items"))
        )
    ):
        raise ProviderOutputError(operation, "schema_echo")
