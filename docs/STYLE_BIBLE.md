# Coherent style bible

`ai/app/services/style_bible.py` defines the versioned `style-bible-v1` contract
shared across character reference, draft sketch, final line art, and palette suggestion.
Style presets resolve raw string IDs into explicit visual directions:
line treatment, environment, materials, tonal values, palette guidance, lighting,
mood, depth staging, and negative constraints.

## Presets

Five standard style presets are defined with checked-in golden fixtures under
`ai/tests/fixtures/style_presets/`:

| Style ID | Label | Dominant tones | Key finish characteristics |
| --- | --- | --- | --- |
| `classic-comic` | Classic Comic | Deep navy, newsprint white, ink black | Dynamic brush lines, halftone dots, high-contrast comic lighting, 3-plane staging |
| `cyberpunk-noir` | Cyberpunk Noir | Midnight obsidian, cyan-slate, charcoal | Sharp technical ink, wet reflections, hot neon rim lighting, chiaroscuro contrast |
| `classic-superhero` | Classic Superhero | Cobalt blue, charcoal, pure white | Muscular contours, cross-feathering, heroic overhead key, bold primary palette |
| `manga-ink` | Shonen Manga Ink | Sumi black, 50% screentone, pure white | Expressive G-pen inking, mechanical halftones, cross-hatch shadows, speedline depth |
| `indie-graphic-novel` | Indie Graphic Novel | Slate blue, terracotta, warm parchment | Organic dip-pen linework, paper tooth, gentle midtone washes, muted earthy tones |

Unknown or custom style strings resolve deterministically to a safe fallback
StyleBible incorporating the custom style identifier as inspiration with balanced
production defaults.

## Cross-stage integration

1. **Character turnaround (`CharacterService`)**: Incorporates the style bible's
   line treatment, materials, palette tones, lighting, mood, depth, and exclusions
   into the reference sheet prompt.
2. **Draft sketch (`DraftService`)**: Guides composition and staging from the style
   bible's depth and mood parameters, and embeds the complete resolved `style_bible`
   object into `page-render-brief-v1`.
3. **Final line art (`LineArtService`)**: Uses the style's line and tonal values
   directly in stage inking instructions. This removes blanket "flat white fills, no shading"
   instructions that previously conflicted with screentone, cross-hatching, and chiaroscuro styles.
4. **Colour palette (`ColourService` and vision adapter)**: Enriches the vision model's
   panel context with the style's theme, dominant and accent tones, temperature, and
   suggested region harmonies.

## Deduplication and stability

`build_responses_image_request` avoids appending duplicate `Style: {style}` strings
when the prompt or render brief already incorporates the style bible.
Every preset computes a stable SHA-256 hash (`bible_hash`) from its canonical
key-sorted JSON serialization.
