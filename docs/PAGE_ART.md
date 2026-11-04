# Deterministic page-art cleanup

`ai/app/services/page_art.py` removes confidently isolated lettering outside an enclosed panel grid. It uses Pillow, already a locked runtime dependency, and Python connected-component analysis. It performs no OCR, network requests, or additional image generations. Prompts are unchanged.

Both `DraftService` and `LineArtService` call `store_page_image` immediately after the provider returns. This is the single cleanup seam: one invocation per generated page image, before its working copy is stored. Character references use the original `store_image` path. Colouring, subsequent reference inputs, and PDF assembly read the stored working copy; export does not run cleanup again.

When pixels change, the untouched provider bytes are stored at the same key stem with `_raw` appended, retaining the source extension and content type. For example, a JPEG response becomes `pages/<id>/line_art_page_1_raw.jpg` plus a cleaned `line_art_page_1.png`. The provider result and provenance metadata are never mutated. Embedded cryptographic credentials belong to the raw source; the PNG is an edited working derivative. An abstention stores the original bytes and media type without re-encoding or creating another object.

Cleanup blanks local text rectangles with the median corner paper colour. The canvas dimensions, aspect ratio, panel coordinates, and decoded pixels outside those rectangles stay identical. Keeping the original canvas avoids resampling or moving real art and preserves colour-editor coordinates. There is no crop, stretching, or automatic panel reflow. Separate margin drawings and in-panel speech bubbles remain artwork.

## Sample and calibration

The regression fixture is the actual line-art image extracted with `pdfimages -all` from the two-page `live-latest.pdf` produced on 2026-11-04. That source image is 1920 x 1280. It visibly contains a heading, four bordered panels, and a bottom NOTES block. Rendering page two with `pdftoppm` independently confirmed these marks were baked into the image rather than PDF text.

The committed fixture is a metadata-free 768 x 512 grayscale reduction (204,795 bytes, below 300 KB). Its text describes the fictional comic character and scene; it contains no account, run identifier, or private user text. See [fixture provenance](../ai/tests/fixtures/page_art/README.md). `latest.pdf` was also inspected, but its embedded image was byte-identical to `live-latest.pdf`, so it adds no independent sample.

At fixture resolution, panel bounds are approximately x=75..693, y=45..455. The heading has 19..20 px tall glyphs at y=9..29, with thin underlines down to y=37. Notes use approximately 5..11 px glyphs at y=460..511. Border-edge coverage is above 98%; the largest interior drawing components have only 17..60% coverage at their bounding edges. These measurements motivate the separation and confidence thresholds below. They are conservative calibration on one observed page, not a claim of broad OCR accuracy. The original 1920 x 1280 image was also cleaned and exported locally without paid calls, with exact panel-pixel comparison.

## Detection thresholds

Analysis scales down only the detection copy to at most 1024 x 1024. The working artwork is never resampled. All pixel thresholds below refer to that analysis copy; output rectangles map back with outward rounding.

| Gate | Threshold and purpose |
| --- | --- |
| Input | Single frame; each dimension at least 160 px; at most 16 million pixels. RGB, grayscale, palette, and fully opaque RGBA are supported. Transparent or other pixel modes abstain. |
| Paper | Each corner channel must be at least 235. Median RGB corner values supply the blank colour. Non-paper corners cause abstention, including typical full-bleed art. |
| Ink | Grayscale below 160; four-connected dark pixels form strokes. This separates dark line art from paper grain and light antialiasing. |
| Panel rectangle | Component width and height each at least 18% of the image; all four edges have at least 90% dark-pixel coverage within strips of width `max(2, round(min_dimension * .008))`. This rejects large irregular drawings. |
| Grid | Union of enclosed rectangles spans at least 55% of both image dimensions. It must clear every image edge by more than `max(3, ceil(min_dimension * .004))` px. |
| Search region | Only four bands outside that grid, separated by the same guard distance. Panel interiors and gutters are never candidates. Components crossing a search boundary protect intersecting candidates. |
| Glyph candidate | Height `max(4, image_height * .008)` through `image_height * .055`; width .12..3 times height; at least 3 ink pixels and 8% bounding-box density. This covers the observed heading and small notes. |
| Text-line evidence | At least five glyph candidates spanning four median glyph heights. Relative heights .65..1.5, baseline distance at most .4 seed height, and gaps at most four seed heights. The seed must be .8..1.25 of the group median; total baseline spread must be no more than `max(2, .35 * median_height)`. This rejects loose clusters of small drawings. |
| Line completion | Grow a confirmed line through nearby lowercase strokes and punctuation, up to three median heights away. Ordinary strokes may be 2.1 median heights tall; narrow joined strokes may be four heights tall if no wider than two heights. The latter handles the observed connected strokes joining adjacent notes lines. |
| Art protection | Reject candidate rectangles intersecting crossing components, structures wider than six glyph heights, or tall structures exceeding the joined-stroke limits. This retains enclosed bubbles and large drawings. |
| Small padding | 18% of seed height (at least 1 px), then 1 px for antialiasing, clipped to the safe band. |
| Editorial rules | Include thin horizontal strokes immediately below a confirmed line, at most 20% line height thick (minimum 3 px), at least .8 line height wide, and within .4 line height below it. Rule bounds receive another 15% horizontal allowance for their faint endpoints. |

The service logs a reason code and removed rectangles, without recognizing or logging any text. Abstention reasons include `no-enclosed-panel-grid`, `small-or-ambiguous-panel-grid`, `panel-grid-touches-margin`, `no-confident-margin-lettering`, `non-paper-or-full-bleed`, `transparent-paper`, `unsupported-paper-mode`, `unsupported-dimensions`, `animated-image`, and `undecodable-image`. Successful cleanup reports `isolated-margin-lettering`.

## Limits and failure modes

- Borderless pages, broken or slanted borders, tiny panels, lettering touching art, and uncertain geometry remain unchanged. Full-bleed and border-touching pages deliberately abstain even when they contain text.
- In-panel lettering is preserved, including unwanted captions: geometry cannot establish whether it is dialogue or an editorial note.
- A margin caption enclosed by a bubble or connected to a drawing may be retained. Thin broken outlines can change that decision at different analysis resolutions. A shape-only detector cannot infer editorial intent from such a callout.
- Fewer than five glyphs, rotated lettering, very faint text, unusual scripts, connected handwriting, and fonts outside the measured size range can be missed.
- A row of small drawing marks with the same sizes, spacing and baseline as text can be mistaken for lettering. The panel-grid restriction and structure checks reduce this risk but do not prove semantic intent. Raw bytes remain available for recovery.
- Paper texture inside blanked rectangles becomes flat. Faint compression ringing outside the detected text rectangles can remain. Outside those rectangles even JPEG/WebP decoded pixels are preserved by encoding the derivative losslessly as PNG.
- Existing stored pages and already-created PDFs are not retroactively rewritten. New page generations go through this seam; exported working copies are not processed a second time.

## Hermetic verification

`ai/tests/unit/test_page_art.py` covers the real heading and every notes line, exact preservation of all panels and separate margin drawings, and exact preservation outside reported rectangles. It also drives both generation services into storage and PDF assembly, asserting one provider call and one cleanup invocation, unchanged raw bytes/provenance, and the cleaned RGB pixels embedded in the PDF. Scale tests exercise detection above the 1024 px analysis limit. JPEG, WebP, opaque RGBA, and palette inputs exercise lossless working-copy conversion.

Synthetic preservation cases cover a text-free grid, panel dialogue in a speech bubble, thick borders touching the image edge, full bleed, margin drawings, and a bubble crossing a panel border. Positive cases cover isolated horizontal text in each of the four margin bands. All fixtures and tests run without credentials or external services.

Run the complete AI and end-to-end suites using [the CI guide](CI.md):

```bash
make guard-hermetic lint-ai test-ai test-e2e
```
