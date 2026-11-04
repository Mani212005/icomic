"""Shared Meta pricing, conservative estimates, and observed usage costs."""

DEFAULT_REASONING_RESERVE_TOKENS = 1024

PRICE_INPUT_TOKEN_PER_M = 1.25
PRICE_CACHED_INPUT_TOKEN_PER_M = 0.15
PRICE_OUTPUT_TOKEN_PER_M = 4.25
PRICE_PER_IMAGE_FLAT_USD = 0.01

WORST_CASE_TEXT_INPUT_TOKENS = 60
WORST_CASE_TEXT_OUTPUT_TOKENS = 32
WORST_CASE_VISION_INPUT_TOKENS = 350
WORST_CASE_VISION_OUTPUT_TOKENS = 64


def calculate_token_cost(
    input_tokens: int, output_tokens: int, cached_input_tokens: int = 0
) -> float:
    input_cost = (
        input_tokens - cached_input_tokens
    ) * PRICE_INPUT_TOKEN_PER_M + cached_input_tokens * PRICE_CACHED_INPUT_TOKEN_PER_M
    output_cost = output_tokens * PRICE_OUTPUT_TOKEN_PER_M
    return (input_cost + output_cost) / 1_000_000.0


def estimate_call_worst_case(
    stage: str, reserve_tokens: int = DEFAULT_REASONING_RESERVE_TOKENS
) -> float:
    if stage == "language":
        return calculate_token_cost(
            WORST_CASE_TEXT_INPUT_TOKENS, WORST_CASE_TEXT_OUTPUT_TOKENS + reserve_tokens
        )
    if stage == "vision":
        return calculate_token_cost(
            WORST_CASE_VISION_INPUT_TOKENS, WORST_CASE_VISION_OUTPUT_TOKENS + reserve_tokens
        )
    if stage == "image":
        return PRICE_PER_IMAGE_FLAT_USD
    return 0.0


def calculate_checklist_worst_case(
    reserve_tokens: int = DEFAULT_REASONING_RESERVE_TOKENS,
) -> float:
    return sum(
        estimate_call_worst_case(stage, reserve_tokens if stage != "image" else 0)
        for stage in ("language", "vision", "image")
    )


def cost_from_usage(stage: str, usage: object | None) -> float:
    if stage == "image":
        return PRICE_PER_IMAGE_FLAT_USD
    if usage is None:
        return 0.0
    input_tokens = int(getattr(usage, "input_tokens", 0))
    output_tokens = int(getattr(usage, "output_tokens", 0))
    return calculate_token_cost(
        input_tokens, output_tokens, int(getattr(usage, "cached_input_tokens", 0))
    )


def actual_stage_cost(stage: str, adapter: object, model_id: str = "") -> float:
    if stage == "image" and model_id.endswith("-fake"):
        return 0.0
    return cost_from_usage(stage, getattr(adapter, "last_usage", None))
