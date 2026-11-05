# Script pacing and complete comic generation

The project panel budget follows the complete script, independently of the chosen
chapter count. Scripts under 3,000 words receive `max(4, ceil(words / 60))` panels.
Scripts from 3,000 through 6,000 words receive `50 + ceil((words - 3000) / 300)`
panels. Longer stories continue growing at `ceil(words / 100)` panels. There is
no project or chapter panel ceiling. The automatic chapter suggestion allocates
about fifteen panels per chapter. An explicit chapter selection remains supported.

The default layout holds at most **five panels per page**, balancing chapter
panels across pages so normal pages have four or five panels. A very short scene
or a large explicit chapter count can produce smaller pages. The existing
`MAX_PANELS_PER_PAGE` operator configuration remains supported; four to six is
recommended. The comic runner verifies the default five-panel layout. A cover is
additional to the comic pages.

## Audit of limits and lists

| Area | Previous limitation | Current behavior |
| --- | --- | --- |
| Go project defaults | One chapter regardless of script | Suggest chapters from complete word count |
| Python analysis | Ten chapter ceiling; long-script warning | Uncapped suggestion; analyze every source chunk |
| Outline generation | One model call, discarded output | Structured outline calls per chapter/source chunk; combine all parts |
| Panel generation | Hard-coded four panels; discarded model output | Explicit chapter panel budget; structured page batches of at most five panels |
| Provider output budget | 32 tokens for story responses | Independent 4,096-token structured story cap; 16,384-byte input bound conservatively bounds input tokens; malformed/short output fails explicitly |
| Input partitioning | Whole script in one response request | At most 500 source words per analysis/outline/moderation call; page source partitions preserve every word |
| Go and Python contracts | No requested panel budget or chapter source | Optional `panel_count` and `chapter_script`; legacy direct requests retain four-panel default; no array item ceiling |
| Go response validation | Accepted partial panel lists | Reject incomplete panel counts before persistence |
| Page allocation | Four-panel default | Five-panel default; all panel IDs assigned once; no page-count ceiling |
| Runner | Forced one chapter and generated only first chapter | Generates, persists, and checks every chapter, panel and page; accepts `--script-file` |
| Editor | Fetched only first chapter; chapter inputs capped at ten | Loads all chapter lists, labels chapter/page context, removes chapter input cap, exposes missing outline/panel generation actions |
| PDF/export | Page numbers restarted for each chapter; count check allowed missing cover/pages | Export page numbers span all chapters; runner requires exact comic pages plus one cover; assembly loops over every supplied page |
| Quota | Image exhaustion also rejected text, palette and export jobs | Image quota applies to image attempts; kill switch, spend protection and credits still apply to every generation |

The Go fake client honors the requested panel budget, and the Python fake language
adapter returns deterministic structured responses whose scene descriptions cover
the entire source text in order. Contract models are regenerated from OpenAPI.
No default adapter selection changes: fakes remain the default.

Story calls send their Pydantic JSON schema through the provider's
`response_format` with `type: json_schema`, as documented in the
[Meta structured-output guide](https://dev.meta.ai/docs/structured-output).
Characters and panels retain their array root; outlines retain their object root.
The default non-strict schema mode supports these existing schemas. Local model
validation still rejects invalid or incomplete output without retrying. Capped
calls include the same schema in the token-count preflight as `text.format`, so
schema overhead is included before reserving provider cost. Failed generation
jobs retain observed provider costs even when output validation fails and user
credits are refunded.

Character, outline, panel and palette parsing reject schema documents, incorrect
root types and incomplete JSON with a non-retryable `ProviderOutputError`.
Palettes require a nonempty theme and valid named hex colours; invalid output no
longer silently becomes a default palette. A `length` finish reason fails even
when its partial text happens to parse as JSON. Corrupt PNGs fail PDF export
instead of becoming placeholder artwork. Successful image responses are stored
using their actual byte format, including WebP with a misleading PNG label.

## Fixture and repeatable proof

`tests/fixtures/the_tide_clock.txt` is an original 3,130-word story. Its default
plan is 51 panels, four chapters, twelve comic pages, and thirteen PDF pages.
Each runner invocation provisions a fresh PostgreSQL user and opening credit.
It asserts generated and persisted counts, complete panel assignment, and the exact
PDF page count. The default hermetic E2E suite separately exercises this same
runner against isolated in-memory Go state and the real Python service on fakes.

With local fake services and PostgreSQL already running:

```sh
make e2e-comic E2E_ARGS="--skip-services --script-file tests/fixtures/the_tide_clock.txt --artifact-dir /tmp/icomic-long"
```

Omitting `--script-file` retains the four-panel short smoke story. No live adapter
or credential is required for either flow.

## Calls for this fixture, without retries or regeneration

Let `C` be the number of main characters receiving automatic references, at most
six; every other extracted profile remains an approved extra without an
automatic reference. See [generation policy](GENERATION_POLICY.md). The fake
fixture has `C = 1`; a live model can extract more characters, so reference calls
must be expressed in terms of `C`. There are 51 panels and twelve comic pages.
Moderation is a language-provider call and is listed separately to avoid counting
it twice. The table counts successful first attempts; transport retries would add
calls. There are no per-panel image or vision calls.

| Operation | Language | Image | Vision | Moderation |
| --- | ---: | ---: | ---: | ---: |
| Script analysis (seven source chunks) | 7 | 0 | 0 | 7 |
| Character references | 0 | C | 0 | C |
| Chapter outlines (two chunks per chapter) | 8 | 0 | 0 | 7 |
| Panel text (one call per planned page) | 12 | 0 | 0 | 8 |
| Drafts (one image per page) | 0 | 12 | 0 | 12 |
| Line art (one image per page) | 0 | 12 | 0 | 0 |
| Palette suggestions (one vision call per page) | 0 | 0 | 12 | 0 |
| Export and approval | 0 | 0 | 0 | 0 |
| Total | 27 | 24 + C | 12 | 34 + C |
| Total with fake fixture's single profile | 27 | 25 | 12 | 35 |

Per-page generation uses one language call, two image calls, one vision call and
one draft moderation call. Chapter/source moderation is additional as shown.
Across the whole one-character fixture, normalized counts per panel are
`27/51` language, `25/51` image, `12/51` vision and `35/51` moderation;
per comic page they are `27/12`, `25/12`, `1`, and `35/12` respectively.
Including moderation, the language provider receives `61 + C` calls in total.
Page image calls attach only named references selected from that page's visual panel fields, plus the relevant composition/style inputs; URL-only legacy callers supply their own selected references.
Conditioning limits and payload accounting are documented in [the Meta guide](META_SWAP.md#upload-response-and-conditioning-budgets).
Attachment selection does not change the flat image fee, image-call count or reserved image cost in this inventory.
Live counts assume concise structured responses obeying the prompt: each combined
chapter outline and its source fit two moderation chunks, and each page's scene
and dialogue fit one. More verbose user edits can require additional moderation
chunks. This is a call inventory, not a cost estimate.

## Image budgets

The runtime policy defaults to 40 images per project/book, 80 per user over 24 hours and 800 over 30 days. A 51-panel, 12-page book needs about 30 images: 12 drafts, 12 final line art and main-character references. The book budget leaves revision room, and the daily cap permits two books.

Each new image generation, regeneration or image edit counts once. Replaying an idempotency key never counts again. Failed or cancelled attempts count until a full refund commits; that refund releases all three budgets once. Partial refunds retain the image count. Approvals, colouring uploads, moderation and language calls do not count. Deleting a project retains daily/monthly usage. See ARCHITECTURE section 5.5 for transaction and upgrade details. Elevated runner quotas must remain confined to an explicit e2e profile on an isolated database; production defaults stay 40/80/800.
