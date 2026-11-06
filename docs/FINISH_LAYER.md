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
| `paper` | Adds three scales of tooth and directional fibre variation, weighted more strongly in light regions. | Shadows retain detail and receive less texture than paper highlights. |
| `halftone` | Adds a 23-degree dot screen in shadows and flat regions. | A local edge mask suppresses the pattern around detailed contours and faces. |
| `registration` | Moves color channels in a seeded, stable direction by a fraction of a pixel, capped at 0.95 px at 1280 px width; adds slight dark contour spread. | The displacement scales with page width and is dialed down in the combined presets. |
| `matte` | Applies a gentle S curve, slight saturation compression, lifted blacks, and a warm paper white. | Highlight tint and shadow lift are intentionally restrained. |
| `grain` | Adds fine seeded scan grain weighted by luminance. | Grain is softer in shadows than highlights. |
| `print` | Combines paper, halftone, registration/bleed, matte, and grain. | Tuned as the subtle, page-wide option. |

`FinishStrength` has five levels. `off` returns the original bytes; `light` uses
48 percent of the current `print` component settings; `print` is the subtle
baseline; `plus` starts at 1.75 times the baseline texture and raises screen
visibility, ink compression, and grain; `heavy` starts at 2.5 times baseline
and pushes those effects toward a zine or risograph look, including more visible
registration. The combined preset caps registration at 0.95 px at 1280 px
width. White is capped below pure white for combined and matte treatments. All
levels remain deterministic for the same bytes, seed, and library versions.

In the phase-one comparison, `plus` is the recommended candidate for review:
the surface reads clearly at screen size and faces remain crisp. `heavy` is an
upper bound for the visual experiment; its grain and color fringes start to
compete with faces and fine outlines.

The manual generator is `scripts/manual/finish_layer_sheet.py`; it reads the
existing painted pages under `/home/mani/icomic-artifacts/long-run-final-2/images/`
and writes into `/home/mani/icomic-artifacts/finish-layer/`. Its new
`strength-ladder-comparison.jpg` lays out original, print, print-plus, and
print-heavy at page scale with source-pixel crops. The
`strength-ladder-screen-1280.jpg` is exactly 1280 px wide and includes native
pixel detail crops. Pass `--refresh-phase-one` to regenerate the original
component experiment sheets as well.

## What texture cannot fix

Texture cannot make smooth, waxy skin read as naturally painted, or repair faces that change between panels.
It cannot correct odd hands, duplicate props, broken anatomy, or inconsistent perspective; those need better source art.
