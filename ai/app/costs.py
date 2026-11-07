"""Shared Meta pricing, conservative estimates, and observed usage costs."""

from dataclasses import dataclass
from types import MappingProxyType

DEFAULT_REASONING_RESERVE_TOKENS = 1024

PRICE_INPUT_TOKEN_PER_M = 1.25
PRICE_CACHED_INPUT_TOKEN_PER_M = 0.15
PRICE_OUTPUT_TOKEN_PER_M = 4.25
PRICE_PER_IMAGE_FLAT_USD = 0.01

WORST_CASE_TEXT_INPUT_TOKENS = 60
STORY_INPUT_TOKEN_LIMIT = 16384
WORST_CASE_VISION_INPUT_TOKENS = 350
# A page downscaled to at most 1024 px on the long edge, plus the subject prompt.
WORST_CASE_SUBJECTS_INPUT_TOKENS = 2048


@dataclass(frozen=True)
class GenerationPolicy:
    answer_tokens: int
    reasoning_reserve: int
    effort: str
    observed_completion: int
    reasoning_bound: int
    input_bound: int

    def options(self, reserve_floor: int = 0) -> dict:
        return {
            "max_tokens": self.answer_tokens,
            "reasoning_reserve_tokens": max(self.reasoning_reserve, reserve_floor),
            "reasoning_effort": self.effort,
        }


# Evidence: data/icomic-night-d-live-run/live-attempt-3-provider-costs.json and
# data/icomic-live-long/provider-costs-{30e951...,040cd4...}.json. Successful
# historical ledgers did not retain reasoning separately, so their maximum FULL
# completion counts are conservative reasoning upper bounds, not measurements.
# The failed moderation diagnostic response-2012b255... records 873 reasoning
# tokens, 1056 completion tokens and 797 visible characters exactly. See
# docs/GENERATION_POLICY.md for full paths, margins and the replay distinction.
GENERATION_POLICIES = MappingProxyType(
    {
        "moderation": GenerationPolicy(256, 2048, "minimal", 1056, 873, STORY_INPUT_TOKEN_LIMIT),
        "analysis": GenerationPolicy(4096, 2560, "low", 1995, 1995, STORY_INPUT_TOKEN_LIMIT),
        "outline": GenerationPolicy(4096, 1280, "low", 762, 762, STORY_INPUT_TOKEN_LIMIT),
        "panels": GenerationPolicy(4096, 1536, "low", 1056, 1056, STORY_INPUT_TOKEN_LIMIT),
        "palette": GenerationPolicy(512, 1280, "low", 809, 809, STORY_INPUT_TOKEN_LIMIT),
        "check_language": GenerationPolicy(
            256, 2048, "minimal", 64, 62, WORST_CASE_TEXT_INPUT_TOKENS
        ),
        # No separately retained live review reasoning; use the vision completion bound.
        "review": GenerationPolicy(256, 1280, "minimal", 809, 809, WORST_CASE_VISION_INPUT_TOKENS),
        # One JSON list of up to 40 boxes; reasoning stays minimal. Bounds are conservative
        # reuse of the review reasoning evidence until a live call records its own usage.
        "subjects": GenerationPolicy(
            1024, 1280, "minimal", 809, 809, WORST_CASE_SUBJECTS_INPUT_TOKENS
        ),
    }
)

# Compatibility aliases derive from the same table, never independent wire caps.
STORY_OUTPUT_TOKEN_LIMIT = GENERATION_POLICIES["analysis"].answer_tokens
WORST_CASE_TEXT_OUTPUT_TOKENS = GENERATION_POLICIES["check_language"].answer_tokens
WORST_CASE_VISION_OUTPUT_TOKENS = GENERATION_POLICIES["review"].answer_tokens


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
    stage = {"language": "check_language", "vision": "review"}.get(stage, stage)
    if stage in GENERATION_POLICIES:
        policy = GENERATION_POLICIES[stage]
        return calculate_token_cost(
            policy.input_bound, policy.answer_tokens + max(policy.reasoning_reserve, reserve_tokens)
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
