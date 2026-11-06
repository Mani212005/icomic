# Finish layer experiment

This phase-one experiment applies a deterministic, seeded print treatment to a
painted image buffer. It is intentionally a pure image operation and is not
connected to generation, lettering, or PDF export. Lettering can be rendered
afterward as vector art. The function accepts encoded PNG or JPEG data, preserves
the decoded dimensions and supported mode (L, RGB, RGBA), and returns encoded
image bytes. An optional same-size grayscale mask limits the effect to painted
art; masked-out pixels remain unchanged. `off` returns the original bytes
exactly.

```python
from app.services.finish_layer import FinishStrength, finish_image, page_seed

finished_png = finish_image(
    source_png,
    seed=page_seed("book-id/page-03"),
    strength=FinishStrength.PRINT,
)
```

The string `page-03` or another stable page identifier can be passed directly as
the seed. `page_seed()` hashes identifiers to a stable 64-bit integer. Reusing
the same input bytes, seed, strength, treatment, and library versions gives the
same output bytes.

## Candidate treatments

| Treatment | What it does | Safeguards |
| --- | --- | --- |
| `paper` | Adds three scales of low-amplitude tooth and directional fibre variation, weighted more strongly in light regions. | Texture is under two percent at full strength and remains faint in shadows. |
| `halftone` | Adds a 23-degree dot screen in shadows and flat regions. | A local edge mask suppresses the pattern around detailed contours and faces. |
| `registration` | Moves color channels in a seeded, stable direction by a fraction of a pixel, up to 0.72 px at 1280 px width; adds slight dark contour spread. | The displacement scales with page width and is dialed down in the combined preset. |
| `matte` | Applies a gentle S curve, slight saturation compression, lifted blacks, and a warm paper white. | Highlight tint and shadow lift are intentionally restrained. |
| `grain` | Adds fine seeded scan grain weighted by luminance. | Noise standard deviation is about 1.35 levels at full strength. |
| `print` | Combines paper, halftone, registration/bleed, matte, and grain. | Each component is reduced relative to its standalone version to avoid an obvious filter. |

`FinishStrength` has `off`, `light` (48 percent of the calibrated effect), and
`print` (full calibrated effect). The manual comparison generator is
`scripts/manual/finish_layer_sheet.py`; by default it reads the existing painted
pages under `/home/mani/icomic-artifacts/long-run-final-2/images/` and writes
the sheets and page variants to `/home/mani/icomic-artifacts/finish-layer/`.

## What texture cannot fix

Texture cannot make smooth, waxy skin read as naturally painted, or repair faces that change between panels.
It cannot correct odd hands, duplicate props, broken anatomy, or inconsistent perspective; those need better source art.
