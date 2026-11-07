# Deterministic lettering, stages 1 through 4

`ai/app/services/lettering/` is a hermetic vector layout library. It accepts
`LetteringDocument` and returns `LetteringLayout`; `layout_bytes` produces canonical
UTF-8 JSON. Stage 2 connects it to new page generation and versioned page storage.
The document contains no image bytes or URL. The internal layout request may supply
the stored text-free painted image URL; the service reads it through object storage
and analyzes pixels with Pillow and NumPy, without a provider call. The editor draws
and edits the resulting vector layer. PDF export draws that same stored vector layer.

## Enablement and legacy safety

`LETTERING_OVERLAY_ENABLED=true` on the Go API is the **single opt-in setting**.
It defaults to false, including in Compose, and remains opt-in after all four stages.
When enabled, newly created projects and their newly allocated pages record
`lettering_mode: overlay`. Everything else is `baked`. Mode is fixed at creation,
not inferred from prompts or image content. Turning the setting on does not upgrade
existing projects or pages. Even future allocations in the existing running book
stay baked. Migration `000009` only adds columns, defaulting existing rows to baked;
it never updates, downloads, relabels or overwrites existing images.

Overlay drafts and painted pages use a `text_free` document. Ink pages always stay
baked: requesting ink for an overlay page returns 409 rather than consuming a
text-free draft through the legacy lettering prompt. GET on legacy/ink pages
returns baked with no layer; PATCH returns 409. There is no client mode toggle.
Exports draw eligible overlay layouts over the unchanged image. Baked/ink pages
keep their existing comic content streams. The flag remains off by default.

## Composition and text-free rendering

The baked render brief and its v1/v4/v5 fixtures are unchanged. Overlay requests use
`page-render-brief-v6-overlay` with an explicit portrait 2:3 aspect, page-normalized
panel rectangles, and panel-local safe zones. One/two panels use a column; larger
pages use two columns in array order, with the last odd panel spanning both.
Page margins are 3% horizontal/2% vertical, gutters 1.5% horizontal/1% vertical.

Each nonempty panel initially reserves an upper band at `(0.04, 0.04)` with width
`0.92`. Its height is `min(0.80, 0.55 + max(0, characters-50)*0.001 +
max(0, utterances-1)*0.10)`. Counts include narration and speech. The art prompt
requests a large low-detail empty sky, sea or wall pocket at the outer top of each
dialogue panel, away from faces, mouths, hands, birds, props and character silhouettes,
with a clear path for each speech tail. After painting, the layout service scores local edge
density and variance, detects face-like warm-color clusters, and searches within
reserved zones for a calm position. Subject detail is a no-go area even when it is
not recognized as a face. In overlay mode, panel text generation limits dialogue
and captions to 12 words each, allows at most two short speech turns and one caption,
and omits picture-description narration. The parser rejects text beyond that limit;
layout overflow remains a review flag and lettering is never shrunk below 35 pt.
If no calm face-safe position exists, the layout records `no_calm_zone`; the exporter
withholds that utterance and any other unsafe bubbles, draws independently safe
bubbles, and adds a visible review warning. Other layout issues withhold the layer.
This deterministic image analysis complements the prompt rule and does not call a provider.
Unrenderable/crowded text keeps review issues; the system never shrinks below
35 pt to hide a mismatch. Editorial zone edits can instead reserve an empty side.

Source narration becomes one narration utterance first. Nonempty explicit dialogue
lines follow in source order as speech. A leading `Speaker:` is stored separately
from the spoken words; otherwise speech is anonymous. IDs use the persistent panel
ID plus `-narration` or `-dialogue-<source-line-index>`, so text and speaker edits
keep overrides, while removing a line removes its unused override. The schema also
supports thought/caption inputs for future structured editing.

The ordered utterances, kinds and speakers are sent only to the deterministic
layout endpoint. The image brief removes dialogue/narration fields and contains
only geometry/zones plus visual story inputs. Its policy explicitly requests
**TEXT-FREE art: no speech bubbles, no captions, no lettering, no numbers, no signs
with readable text, and no letters, words, numbers, signs, labels, symbols, writing
or glyph-like marks on props and scrolls**. Dialogue panels request a large low-detail
empty pocket at the outer top, away from faces and mouths, with a clear tail path.
Draft and painted stages use the same geometry; legacy ink
never receives an overlay document. No image options or extra provider calls are
added. The fake image adapter returns a portrait 256x384 text-free stand-in for
this mode; baked fake output is unchanged.

## Page storage and API

`pages.lettering` is JSONB, alongside a persisted `lettering_mode`. Its generated
`PageLetteringState` model contains `version: page-lettering-v1`, `input` (the
`lettering-v1` document with no manual placement), `overrides` keyed by utterance
ID, `zone_overrides` keyed by panel ID, and the computed `lettering-layout-v1`
`layout`. Source zones and manual placements remain separate from the draw list.

Layout is computed at allocation, after source panel text edits and page allocation
edits, and after zone/utterance override PATCH. Automatic zones follow changed text
demand; manually edited zones persist across text edits. Geometry is validated by
the shared layout service. Source images, URLs, provenance and image approval
timestamps are not changed by a lettering-only PATCH. The image hash e2e test
covers both source text edits and manual placement edits. Lettering-only writes
compare the previous layer and update only JSONB, so concurrent edits return 409
for reload/retry without overwriting images or another placement. Ordinary image
writes cannot change lettering mode/state. Reads and generation reconcile changed
source input after an interrupted text update. Painted-page generation also
recomputes layout against the newly stored art when the lettering document is
unchanged.

The public contract is `contracts/openapi/lettering-api.yaml`; payload schemas and
Go/Python generated models share `contracts/openapi/ai-service.yaml`:

- `GET /api/v1/pages/{id}/lettering` returns `lettering_mode` and, only for eligible
  pages, the complete versioned `lettering` state.
- `PATCH /api/v1/pages/{id}/lettering/utterances/{utterance_id}/override` accepts
  `{"override":{"center":{"x":0.5,"y":0.2}}}`. Other fields are `rect`,
  `tail_anchor`, `size_scale`. An empty override object clears the manual edit.
- `PATCH /api/v1/pages/{id}/lettering/utterances/{utterance_id}/text` edits source
  text and recomputes the layer atomically without changing image approval.
- `PATCH /api/v1/pages/{id}/lettering/zones` accepts `panel_id` and the complete
  `reserved_zones` array in panel-local coordinates. An empty array explicitly
  records missing-zone review issues.

All routes use page/chapter/project ownership checks and public request bounds.
Unknown IDs return 404, another owner returns 403, invalid payloads return 400,
and baked art edits return 409. Out-of-bounds **manual placements** remain exact
and are flagged rather than clamped; reserved zones must fit their panels.
`POST /internal/v1/layout-lettering` is a local deterministic operation, outside
provider queues/jobs/billing. It returns 422 for invalid documents. Recompute
replaces the complete vector layer, never appends another copy.

The source contract is `contracts/openapi/ai-service.yaml`. The standalone
`contracts/schemas/lettering.schema.json` describes both input and output; contract
tests enforce definition parity and validate the golden fixtures. Run
`make generate-contract-models` after contract changes. Python and Go models are
generated, not handwritten.

```python
from app.gen.models import LetteringDocument
from app.services.lettering import layout_page, layout_bytes

document = LetteringDocument.model_validate_json(input_json)
result = layout_page(document)
serialized = layout_bytes(result)
# result.issues contains typed review flags, including issues without a bubble.
```

## Identity, ordering and legacy art

A document has a stable page ID, stable panel IDs and page-unique utterance IDs.
Each panel and utterance has an explicit nonnegative `order`. Panel orders are
page-unique; utterance orders are panel-unique. Arrays may arrive in any order;
layout sorts by explicit order, never by speaker name or ID. Duplicate IDs/orders,
nonfinite values and invalid panel/zone geometry raise `ValueError`.

Utterances contain Unicode text and a kind (`speech`, `thought`, `narration`,
`caption`). `speaker` is an optional character ID or display name; omit it for
narration or an unknown speaker. The engine does not infer labels, strip prefixes,
rewrite source dialogue, or resolve names. An optional speaker anchor identifies
where the tail points in that panel. Panels supply geometry from the page layout
and explicitly reserved safe zones. Empty zones produce `missing_zone` and no
bubble. When painted art is available, deterministic image analysis also rejects
face and subject regions and marks layouts that have no calm placement for review.

`art_mode` is required. Future integrations must classify existing art as
`baked_in`; this mode always returns an empty draw list and `baked_in_art`, even if
utterances are present. Only explicitly `text_free` artwork is eligible. A result
is a complete replacement layer for its page, never an increment to append to an
older layer. The service reads art only for placement safety and never changes
source pixels; source-image hash regressions enforce that boundary.

## Coordinates and renderer contract

All spaces have their origin at the top left, X rightward and Y downward.

| Field | Space |
| --- | --- |
| `page_aspect` | Physical page width / height; A4 portrait is `210 / 297` |
| Panel `rect` | Page-normalized X/width relative to page width; Y/height relative to page height |
| `reserved_zones`, speaker anchor | Panel-normalized, within `[0, 1]`; rectangle endpoints must also fit |
| Placement center/rect/tail anchor | Panel-normalized; values may be outside `[0, 1]` so invalid edits are preserved and flagged |
| Every result point, rectangle and glyph origin | Page-normalized, X relative to page width and Y relative to page height |
| Result font size and rounded-rect radius | Relative to physical page height |

For a panel-local point `(u, v)` and page-normalized panel rectangle `(x, y, w, h)`,
the output point is `(x + u*w, y + v*h)`. Layout works internally in physical
page-height units so a circle stays circular on a nonsquare page. A renderer scales
output X by its page width and output Y by its page height. The output font size
must be multiplied by page height, not panel height.

The draw list contains rounded rectangles (captions/narration), ellipses
(speech/thought), sampled curved tail polygons, thought-tail ellipses, positioned text lines,
font IDs, exact shaped glyph IDs/origins/advances and text bounds. Glyph cluster
indices are Unicode codepoint offsets within the output line. The baseline is
explicit and all glyph Y offsets have already been converted to downward Y.
Glyph IDs refer to the exact bundled font, not Unicode codepoints. Renderers draw
those positioned glyphs; they must not rewrap, reshape, substitute fonts or run
layout again. This also avoids differences between browser and PDF shaping.

Version 1 uses solid white bubble/tail fills, black text and black outlines at
`stroke_width * page height` (default `0.0013`, about 2.5 px at 1280 px portrait width). Draw the union of a speech body and its tail with one outer outline, then
the glyphs, so the tail has an open seam at its base. Thought circles are
separate ellipses. Bounds describe geometry/ink before stroke;
a renderer should keep the outline stroke inside the described bounds at panel
edges. Empty line bounds have a tiny positive extent for serializability.

## Layout and review flags

The default font size is 35 pt at A4 height. `LayoutConfig.minimum_font_size` is
`35 / 841.89` of page height, equivalent to 35 pt on A4 (297 mm), targeting at
least 55px glyph ink height on 1280x1920 portrait art. Configurations cannot lower
that floor. Size scaling below it is clamped to the minimum and flagged
`below_minimum`. Layout never hides overflow by shrinking lettering. Captions may
use up to 94% of panel width within their reserved zone; speech bubbles may use up
to 45% of panel width.
Other paper heights scale proportionately; physical print-size requirements for
other formats must be selected explicitly by their future integration.

Greedy wrapping uses actual shaped advances and preserves hard line breaks.
Whitespace within a paragraph becomes single spaces; source text is unchanged.
Long words and shaping clusters are never split. A target physical bubble aspect
sets the starting wrap width. Bubbles fit the resulting text and padding; speech/thought
ellipses expand by sqrt(2) so text corners fit. Captions/narration may wrap across
up to 94% of panel width within the reserved zone, then trim to their shaped text
width plus padding. They anchor to the zone top (default) or bottom (`caption_edge`).

Manual rect takes precedence over manual center. Both remain exact, including
invalid placements. Scale affects lettering size, not an explicit rect. Overrides
are validated again for overflow, panel bounds, safe zones and collisions. A
center override derives the bubble size normally and centers it on that point.
Manual rectangles reserve space before automatic placement, even when their
utterances occur later in reading order.

Automatic placement prefers the centered, inset position in the earliest reserved
zone row, then follows reading order. Collisions push candidates right or down to previous bubble boundaries
plus a gap; candidates must fit wholly inside a reserved zone. The search is finite
and deterministic, without a pixel grid, randomness or mutable document state.
This is a greedy layout heuristic, not a global packing optimizer. Unresolved
packing returns explicit `overflow`/`collision` flags for editorial review. Exact
manual overrides may intentionally violate automatic reading order.

Tails attach to the actual bubble silhouette facing the manual/speaker anchor.
Automatic speech tails use a broad base and sampled quadratic sides for a short,
tapered, slightly curved shape, at most 2.5% of page height. Unknown speakers get
a short downward cue. Automatic tails may shorten further to avoid text; explicit
manual tail anchors stay exact. Thought trails are short decreasing circles.
If no safe tail exists it is omitted with `tail_crosses_text`. Text checks include
later utterances. Body collisions and invalid manual geometry stay visible and
flagged. `out_of_panel` includes text, bodies and tails; `outside_reserved_zone`
checks bodies. A tail may leave a safe zone while staying inside its panel.

Empty/whitespace text emits no bubble and `empty_text`. Unsupported text emits no
bubble and `unsupported_glyph` or `unsupported_direction`. Other flags attach to
both the emitted bubble and the page issue list. Consumers must surface issues for
review rather than treating a nonempty draw list as publication approval.

## Fonts and Unicode shaping

Runtime shaping uses pinned `uharfbuzz` (HarfBuzz OpenType shaping), with
`fontTools` for cmap inspection. This works independently of Pillow's optional
libraqm support and platform font availability. Glyph positions and actual ink
extents come from HarfBuzz. No external fonts are requested at runtime.

Bundled files are in `ai/app/services/lettering/fonts/`, with their complete SIL
Open Font License 1.1 texts and upstream copyright notices:

- `comic-neue-v1`: [Comic Neue Regular](https://github.com/google/fonts/tree/main/ofl/comicneue),
  57,248 bytes, for supported Latin text and combining accents.
  SHA-256: `a0ee5a37c8b27c4db0700137d928598b1e23b0089e1546a8961909176b779360`.
- `noto-devanagari-v1`: [Noto Sans Devanagari](https://github.com/google/fonts/tree/main/ofl/notosansdevanagari),
  179,876 bytes, used for the complete utterance if it contains Devanagari.
  SHA-256: `e620d3c070ee047593e58540652f102e31ce7860fd0712ddb2782f5a1c24e2b9`.
  This subset retains contextual glyphs and shaping tables; retaining complete
  Devanagari shaping exceeded the preferred 100 KB budget.

The Devanagari font was instantiated at `wght=400, wdth=100` using
`fontTools.varLib.instancer.instantiateVariableFont`, then subset with
`fontTools.subset` 4.60.1, `recalc_timestamp=False`, keeping U+0020-017F,
U+0900-097F, U+200C-200D, U+2018-2019, U+201C-201D and U+2026. A second subset
pass with `hinting=False` retained the same cmap. OpenType layout closure was
kept, including conjuncts and reordered vowel marks. Font bytes and font IDs
are immutable inputs to the v1 golden results; changing either requires deliberate
review of the serialized layouts.

Emoji, unbundled scripts and unsupported controls are refused and flagged rather
than replaced with tofu glyphs. RTL and bidirectional text are flagged: HarfBuzz
shapes runs, but this stage does not implement a Unicode bidi paragraph algorithm.
The complex-script fixture includes Devanagari conjuncts and vowel reordering.
Mixed supported Latin/Devanagari uses the Devanagari font for the whole utterance.

## Verification and follow-ups

`ai/tests/fixtures/lettering/` has input and byte-exact vector golden pairs for
wrapping, speech/tails, long tokens, empty text, multiple speakers, thought,
narration, accented/combining Latin, Devanagari and refused emoji. Unit tests also
cover minimum size, overrides, multiple panels/zones, impossible packing, blocked
tails, source-image hash preservation and seeded properties for bounds,
non-overlap, reading order and determinism. Everything runs in default hermetic
pytest and contract validation, with no key or paid call.

The fake process e2e test covers allocation through text-free draft/painted art,
layout storage/readback, text/zone/override changes, and byte-identical source art.
Run `make test-e2e`; to exercise durable storage, supply a local migrated/seeded
`TEST_DATABASE_URL` as described in docs/CI.md. Tagged `realdb` tests cover migration
up/down and page state round trips. Cost inventory tests remain unchanged.

The standalone fake runner also supports `make e2e-comic E2E_ARGS="--overlay-lettering"`.
It enables the setting for its isolated Compose stack, saves per-page JSON layouts,
checks an override and source-image hashes, exports a PDF and verifies extractable
text, page count and size. It refuses live
mode. The ordinary baked runner retains ink/colour/PDF coverage.

For review-only real-art samples, use the AI environment:

```bash
uv run --project ai python scripts/render_lettering_sample.py source.png layout.json review.png
```

The Pillow renderer consumes serialized body/tail geometry and exact bundled glyph
IDs/origins, with a single union outline, and writes a separate image. It does not
reshape or rewrap. Real-art samples linked in the PR use text-free panels/areas in
`long-run-final/images/comic-01.png` and `comic-12.png`. Existing model bubbles are
left intact, and no new layer is placed over those panels. These are review copies,
not eligibility upgrades to the running book. Compare the new size to adjacent
model lettering and inspect full resolution, especially tails and caption padding.

## Editor preview and placement

Overlay draft and painted previews use `LetteringPreview` and `LetteringLayer`.
Baked/legacy/ink pages keep their original image preview, without lettering or
controls. A frame follows the actual image bounds, and the SVG uses a physical
page-height viewBox with `preserveAspectRatio="none"`: page-normalized coordinates
scale to the same image pixels at every viewport width. Exact font glyph outlines
are positioned from the stored glyph IDs/origins; no browser wrapping or shaping
is involved. Speech bodies and sampled tails form one closed path with an open
seam, and clipped strokes stay inside the serialized bounds.

WOFFs and glyph paths are derived from the immutable engine TTFs. Both WOFF assets
stay below 100 KB, with their complete OFL/copyright notices beside them. The
Devanagari outline atlas loads only when needed. Inline editing uses bundled
`@font-face` fonts; the preview itself uses exact outlines. Regenerate assets with:

```bash
uv run --project ai python scripts/bundle_lettering_fonts.py
```

Click/focus a bubble or choose an utterance to select it, including utterances
that cannot emit a bubble. Drag the body or its tail handle using mouse/touch.
Arrow keys nudge by 0.1% of the page (Shift: 1%); Enter opens inline text, Escape
leaves editing or cancels an unfinished gesture. Size ranges from 0.875 to 3;
the server still enforces its font floor and returns review flags. The toolbar
provides size, edit, reset-to-auto, undo and redo. Flags include page issues without
a bubble and expose readable accessible names. Focus rings remain visible and
controls respect reduced motion.

Placement previews are optimistic and debounced, with pointer capture for touch.
The complete server layout replaces every provisional result, including automatic
neighbours and review flags. Rejected writes restore the confirmed layer and
refresh it before the next placement. Text debounces separately; pending text
flushes when leaving a preview, even while a prior text write is in flight.
Placement history survives switching between editor stages during the session.

`PATCH /api/v1/pages/{id}/lettering/utterances/{utterance_id}/text` accepts
`{"text":"Follow the light!"}`. It updates the corresponding source narration or
dialogue line, preserving an existing speaker prefix and other source lines.
Dialogue must remain one source line; narration can contain hard breaks. Empty
source text can remove an utterance. The service computes before persisting, then
compares the previous source text and vector state and commits both atomically.
Layout or compare failures cannot leave half an edit. It never changes image
columns, image approval timestamps or page freshness. The ordinary panel editor
retains its existing art-staleness behavior.

The offline mock returns baked for existing sample pages. Explicit overlay fixtures
receive recorded layout geometry plus a local placement/text adapter, with simple
Latin metrics for text demonstrations rather than production shaping. No clean
eligible real-art sample is bundled, so the existing samples remain baked. The
hermetic browser fixture at `/test-fixtures/lettering.html` (Vite development mode)
shows a baked sample beside explicitly text-free test artwork. It is never part of
the normal project seed and makes no provider requests. Component tests cover SVG
snapshots, complex-script glyphs, pointer/tail edits, sizing, keyboard nudges,
flags, reset, undo/redo, rejection, stage eligibility and session continuity.

PDF draws the stored vector result without reshaping. Legacy images retain their
existing export behavior, and the opt-in flag remains off by default.

## Vector PDF export and review guard

The Go export route passes `lettering_mode`, the complete stored `PageLetteringState`
and a source-staleness signal to the AI service. Export reads current panel text and
zones but never refreshes or writes the layout. The latest stored override result is
the draw list; image approval/provenance and source/attachment bytes are untouched.

Each newly computed layout includes `source_digest`, a SHA-256 of the effective
input including text, zones and manual placements. Canonical serialization uses
Pydantic defaults, no nulls, sorted object keys, panel/utterance reading order,
UTF-8 and compact JSON. PDF verifies this digest without wrapping or shaping again.
Layouts written before this field existed are stale for export; clear/reapply an
override in the editor to compute a reviewed replacement.

Eligible comic pages use white vector silhouettes with inward black strokes, the
same joined speech-tail boundary as SVG, and positioned PDF text glyphs from the
exact bundled fonts. The exporter subsets each font once across the document,
embeds TrueType outlines, maps CIDs to glyph IDs and includes a ToUnicode map.
Per-line ActualText preserves logical Unicode order for Devanagari conjuncts and
reordered vowel signs. Text stays selectable/searchable in supporting PDF readers.
There is no raster lettering layer and no image recompression caused by lettering.
The cover remains an art-only preview; the comic page contains the lettering.

Missing/stale layouts or unresolved issues other than omitted `no_calm_zone` utterances
(including overflow, collision, out-of-panel, missing zones, unsupported scripts and
minimum-size violations) omit the lettering layer. When safe bubbles remain beside
an omitted no-calm utterance, the PDF draws those bubbles and warns about the withheld
text. The export POST result includes typed `warnings` with
`page_number`, `code` (`stale_lettering` or `lettering_review_required`), `message`
and review `flags`. The export screen shows these messages. The PDF places a
visible `LETTERING NOT EXPORTED` note under the art, so a downloaded review copy
cannot silently pass as publishable. Other eligible pages still export normally.
The existing export-record GET shape is unchanged; warnings are retained in the PDF.

`ai/tests/unit/test_pdf_lettering.py` locks vector operators, checks subset font
structure and Latin/Devanagari extraction using `pdftotext`, compares `pdftoppm`
renders to the independent serialized-layout raster (mean channel error below
14/255 and fewer than 7% of channels differing by 80/255 in lettering regions),
limits synthetic overlay overhead to 30 KB and checks mixed/legacy page streams.
The legacy content golden was captured from the pre-stage-4 writer. All earlier
layout golden geometry is unchanged; only source fingerprints were added.
Install `poppler-utils` locally for PDF tests; CI installs it explicitly.
