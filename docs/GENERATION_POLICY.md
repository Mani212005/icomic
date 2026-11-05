# Generation answer and reasoning limits

`ai/app/costs.py:GENERATION_POLICIES` is the single table for product-stage
answer caps, reasoning reserves and effort. Services pass its options to the
adapters. The adapter adds answer and reserve into the provider's shared
`max_tokens` wire cap. The runner's stage estimates and third-check reserve use
the same table, and the provider guard reserves from the exact token preflight
and actual wire cap before every transmission. Reasoning is already included in
completion usage and is never billed twice. New provider ledgers retain reported
reasoning-token counts separately for future tuning.

| Stage | Answer cap | Reasoning reserve | Wire cap | Effort | Largest recorded completion | Reasoning evidence used |
| --- | ---: | ---: | ---: | --- | ---: | --- |
| Moderation | 256 | 2048 | 2304 | minimal | 1056 | 873 measured in the failed response |
| Analysis | 4096 | 2560 | 6656 | low | 1995 | 1995 conservative upper bound |
| Outline | 4096 | 1280 | 5376 | low | 762 | 762 conservative upper bound |
| Panel text | 4096 | 1536 | 5632 | low | 1056 | 1056 conservative upper bound |
| Palette | 512 | 1280 | 1792 | low | 809 | 809 conservative upper bound |
| Manual text check | 256 | 2048 | 2304 | minimal | 64 | 62 measured in the earlier diagnostic fixture |
| Manual vision review | 256 | 1280 | 1536 | minimal | No separate live review retained | Uses the palette completion upper bound of 809 |

Image generation has a flat per-image cost and no exposed text/reasoning cap.
There are no product text/vision calls with an intended answer cap below 256.
The palette's previous 64-token answer allocation was too small for a structured
palette; it now has 512 tokens. Moderation requests a compact `SAFE` or `BLOCK`
decision with a brief reason, accepts only that decision format, and uses minimal
reasoning. A block decision stops generation; unrecognized output fails rather
than being treated as safe.

Margins over the reasoning evidence are 1175 tokens for moderation, 565 for
analysis, 518 for outline, 480 for panels and 471 for palette/review. Historical
successful cost ledgers retained full completion totals, **not separate
reasoning counts**. Those totals bound reasoning conservatively; they are not
claimed to be measured reasoning usage. The margins and minimal effort improve
headroom but do not guarantee that an arbitrary future completion will fit. Any
`length` finish reason still fails immediately, even with nonempty/valid text.

Recorded sources (local operational evidence, not committed prompts):

- `/home/mani/icomic-linux-home/data/icomic-night-d-live-run/live-attempt-3-provider-costs.json`: maxima of 789 moderation, 678 analysis, 762 outline, 1056 panel and 809 palette completion tokens.
- `/home/mani/icomic-linux-home/data/icomic-live-long/provider-costs-30e9510243294f9ea7fca664642c8ccc.json`: moderation maximum 886 and analysis maximum 1995 completion tokens.
- `/home/mani/icomic-linux-home/data/icomic-live-long/provider-costs-040cd4e19b4c48efb4ed08265e808885.json`: 13 moderation calls averaged 806.46 completion tokens; maximum 1056. Analysis maximum 1734.
- `/home/mani/icomic-linux-home/data/icomic-live-long/diagnostics-040cd4e19b4c48efb4ed08265e808885/response-2012b25584b546c398a18ae88abbfa54.json`: HTTP 200, `finish_reason=length`, 797 content characters, 34 input / 1056 completion / 873 reasoning tokens.
- `ai/tests/fixtures/meta/chat_completion_reasoning_length.json`: earlier diagnostic shape with 17 input / 64 completion / 62 reasoning tokens.

`moderation_length_873.json` preserves the exact observed failure shape, counts
and content length, with synthetic replacement text instead of the echoed
prompt. Its replay always fails. A separate synthetic completed `SAFE` response
with the same recorded token load proves headroom under the new table; it is not
claimed as a successful live response. The long hermetic flow verifies both
outcomes through the real Go API and Python worker without retries.

With defaults, the unspent third-check reservation is **$0.026832500** for manual
text, manual vision review and one image. A larger explicit generic reasoning
configuration remains a conservative floor for manual-check estimates/calls.
Product services explicitly select their table policy; a smaller generic env
reserve cannot silently lower their stage cap. Fake adapters stay the default.

## Main characters and extras

Automatic analysis preserves every deduplicated character profile, but assigns
at most **six** main characters for automatic reference generation. Names with
more mentions in the complete source rank first; extraction order breaks ties.
Profiles carry `visual_attributes.reference_role` (`main` or `extra`). This
deterministic ranking requires no extra provider call. The 12-page fixture needs
24 page images, so six references require 30 images. The product's default daily
quota remains 25; its capacity question is outside this change. The isolated E2E
deployment explicitly configures 100 daily and 1000 monthly images for each fresh
run user. No existing user's counters or persisted policy rows are changed.
Ranking can miss aliases or
implied importance, so all profiles remain editable and extras remain available.

The runner generates at most six automatic reference images and their six
moderation calls. It still approves **every profile**, including extras, before
outlines and draft generation. Existing approval gates remain in place. Extras
receive no automatic reference image; a user can explicitly generate one when
needed and must approve it after generation. Existing profiles without a role
retain their previous reference behavior. Approved references are still reused
for downstream image generation. There is no character truncation in the story.

## Probe and fresh-run policy

The existing runner's default short script exercises all distinct call types
and PDF export without a new probe tool. Operational live use still requires
the existing live authorization, shared hard cap and explicit prior-spend
source. No live probe belongs in tests or CI.

The runner provisions a fresh isolated user/project. Safe reuse of the previous
paid project is not a small flag: it would need verified owner/script/approval
state plus restoration of working image bytes, which local fake storage keeps
in process memory and loses on Compose recreation. This change therefore keeps
fresh runs rather than introduce unsafe resume behavior or silently reuse stale
references. A short probe must pass before the next authorized long attempt.

## Isolated E2E deployment quotas

The runner starts `deploy/docker-compose.yml` plus the dedicated
`deploy/docker-compose.e2e.yml` overlay under a unique
`icomic-e2e-<run-id>` Compose project. The overlay removes fixed container names
and published PostgreSQL/Redis ports, and assigns free loopback ports to the API
and AI service. Each project gets separate PostgreSQL/Redis volumes and its own
network. The default deployment file is unchanged. The overlay requires Compose
support for `!reset` and `!override`; it was validated with local Compose 2.40.3.

Only this isolated profile passes `ICOMIC_E2E_ISOLATED_STACK=1` and
`ICOMIC_E2E_DAILY_IMAGES=100`, `ICOMIC_E2E_MONTHLY_IMAGES=1000` to the backend.
Startup validates the explicit settings and uses them in the same transactional
quota check as the default policy. It does not update `generation_policy` or any
user usage/counter row. The persisted default remains 25 daily and 250 monthly;
the configured E2E enforcement is 100 daily and 1000 monthly. Image attempts are
still counted normally, and the 101st attempt is refused before another job or
debit is committed. Monthly enforcement uses the configured monthly value.

The limits, scope and unique Compose project are printed in the run log and
saved under `quota_policy` in the cost report. `api_url` and `storage_url` identify
the isolated endpoints. `--skip-services` never applies these settings to an
external stack and reports its limits as unchanged. Live mode still requires
managed services, the shared USD cap, the alert and third-check reserve.

The stack remains available after the runner exits for PostgreSQL and working
asset inspection. Any later cleanup must target only that report's named E2E
project, never the default deployment. Existing shared development users and
their counters are not reused or reset.
