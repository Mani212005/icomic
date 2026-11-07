# Icomic brand assets

The shipped direction is **Drawing Ink**: a fountain pen at the edge of the watercolor ink panel, with a red ink line leaving the nib and the frame. The classic nib, slit and breather hole make it read as an artist's tool, while the panel retains the comic-making reference. The favicon is a simplified companion with no slit or highlights, a thicker ink line with a shorter curl and a tighter crop, so the pointed nib, pen body and ink stay readable at 16 px.

## Sources

- `frontend/static/brand/mark.svg` is the active full-detail mark used in the header, loading and empty states. `frontend/static/brand/favicon.svg` is its deliberately simplified small-size companion, and `wordmark.svg` supplies the tracked outlined letters used in the header.
- `frontend/src/lib/design/theme.css` is the palette token source (`--brand-primary`, `--brand-paper`, `--brand-surface`, `--brand-ink`, and `--brand-accent`). Existing UI tokens alias these values. Keep them aligned with the SVG colors.
- `docs/assets/brand/` keeps the three original concepts and both pen-panel concepts. All include light and dark lockups, light and dark marks, and a one-color mark. The two pen-panel concepts also include app-icon tiles and simplified favicons.

The active PNG app icons are raster derivatives of `frontend/static/brand/app-icon.svg`; the manifest uses the 192 px and 512 px copies. The PDF cover uses vector paths so the cover remains portable and independent of web assets; those paths mirror the Drawing Ink mark and are hand-kept in sync.

## Pen-panel directions

- **Drawing Ink** puts the nib at the panel edge and lets a watercolor stroke leave the frame. It is the shipped direction and emphasizes the act of drawing. Its ink line is thin at small sizes, so the favicon (and its alternate copy in `docs/assets/brand/`) draws it thicker and shorter.
- **Crossing Pen** places the pen in front of the panel frame. It was the first recommendation and stays available as an alternate: the diagonal body and pointed nib remain distinct at small sizes while the frame is still visible.

The comparison lockups use hand-built outlined letter paths. Their shared horizontal tracking was increased by about 6% to separate the initial `i` and `c` without changing the letter shapes.

## Swapping concepts

Choose a concept in `docs/assets/brand/`, then copy its mark, favicon and app icon over the active `mark.svg`, `favicon.svg` and `app-icon.svg` sources in `frontend/static/brand/` (and update `wordmark.svg` if the lettering changes). Every other surface reads those files: the header, footer, loading and empty states use `mark.svg`, `app.html` links the favicon, and the PNG icons and manifest derive from the SVGs. Keep the SVG colors synchronized with `theme.css`, regenerate the PNG icon fallbacks and preview sheet, and update the PDF cover mark in `ai/app/services/pdf.py`. The original three concepts and both pen-panel options remain available as alternates; Crossing Pen is the one to restore if Drawing Ink is ever retired.
