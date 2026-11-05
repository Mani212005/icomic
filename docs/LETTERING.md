# Deterministic lettering, stage 1

`ai/app/services/lettering/` is a standalone, hermetic layout library. It accepts
`LetteringDocument` and returns `LetteringLayout`; `layout_bytes` produces canonical
UTF-8 JSON. It has no HTTP endpoint, image input, storage adapter, UI, PDF hook,
provider call, or persistence. No migration is needed in this stage. Existing
`PanelTextItem`, backend panel/page records, prompts and exports are unchanged.

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
bubble: the library never guesses that key art is safe to cover.

`art_mode` is required. Future integrations must classify existing art as
`baked_in`; this mode always returns an empty draw list and `baked_in_art`, even if
utterances are present. Only explicitly `text_free` artwork is eligible. A result
is a complete replacement layer for its page, never an increment to append to an
older layer. Stage 1 cannot apply overlays at all. The engine never reads or writes
image pixels, and the source-image hash regression test enforces that boundary.

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

The draw list contains rounded rectangles (speech and captions), ellipses
(thought), triangular tail polygons, thought-tail ellipses, positioned text lines,
font IDs, exact shaped glyph IDs/origins/advances and text bounds. Glyph cluster
indices are Unicode codepoint offsets within the output line. The baseline is
explicit and all glyph Y offsets have already been converted to downward Y.
Glyph IDs refer to the exact bundled font, not Unicode codepoints. Renderers draw
those positioned glyphs; they must not rewrap, reshape, substitute fonts or run
layout again. This also avoids differences between browser and PDF shaping.

Version 1 uses solid white bubble/tail fills, black text and black outlines at
`0.001 * page height`. Draw the union of a speech body and its tail with one outer outline, then
the glyphs, so the tail has an open seam at its base. Thought circles are
separate ellipses. Bounds describe geometry/ink before stroke;
a renderer should keep the outline stroke inside the described bounds at panel
edges. Empty line bounds have a tiny positive extent for serializability.

## Layout and review flags

The default font size is 14 pt at A4 height. `LayoutConfig.minimum_font_size` is
`12 / 841.89` of page height, equivalent to 12 pt on A4 (297 mm). Configurations
cannot lower that floor. Size scaling below it is clamped to the minimum and
flagged `below_minimum`. Layout never hides overflow by shrinking lettering.
Other paper heights scale proportionately; physical print-size requirements for
other formats must be selected explicitly by their future integration.

Greedy wrapping uses actual shaped advances and preserves hard line breaks.
Whitespace within a paragraph becomes single spaces; source text is unchanged.
Long words and shaping clusters are never split. A target physical bubble aspect
sets the starting wrap width. Bubbles fit the resulting text and padding; thought
ellipses expand by sqrt(2) so text corners fit. Captions/narration span the widest
reserved zone and anchor to its top (default) or bottom (`caption_edge`).

Manual rect takes precedence over manual center. Both remain exact, including
invalid placements. Scale affects lettering size, not an explicit rect. Overrides
are validated again for overflow, panel bounds, safe zones and collisions. A
center override derives the bubble size normally and centers it on that point.
Manual rectangles reserve space before automatic placement, even when their
utterances occur later in reading order.

Automatic placement visits candidate origins in top-to-bottom, left-to-right
order. Collisions push candidates right or down to previous bubble boundaries
plus a gap; candidates must fit wholly inside a reserved zone. The search is finite
and deterministic, without a pixel grid, randomness or mutable document state.
This is a greedy layout heuristic, not a global packing optimizer. Unresolved
packing returns explicit `overflow`/`collision` flags for editorial review. Exact
manual overrides may intentionally violate automatic reading order.

Tails point at the manual tail anchor, then speaker anchor, then the nearest
panel edge. The engine tries body attachment edges in distance order and checks
the entire tail polygon against every text box, including later utterances.
Thought-tail circles are checked too; the final circle is tangent to the exact
anchor. If no safe attachment exists, the tail is
omitted with `tail_crosses_text`; a conflicting manual anchor is never silently
moved. Body collisions remain visible and flagged, never silently repositioned
when pinned. `out_of_panel` includes text, bodies and tails; `outside_reserved_zone`
checks bubble bodies. Tails may leave the safe lettering zone to reach the speaker
but must remain inside the panel.

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

The remaining stages are separate changes:

1. Stage 2: allocate explicit panel geometry/safe zones and ordered utterances in
   the render brief; opt new art into text-free generation, retaining legacy art.
2. Stage 3: editor preview draws the vector result and persists editable placement
   overrides with versioned page-layer state.
3. Stage 4: PDF draws the same serialized vector layer over eligible source art
   without re-running layout or adding a second layer to legacy images.
