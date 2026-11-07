# Icomic brand assets

The shipped direction is **Crossing Pen**: a fountain pen drawn diagonally across the watercolor ink panel. The classic nib, slit and breather hole make it read as an artist's tool, while the panel retains the comic-making reference. The mark uses a simplified nib silhouette in the favicon so it stays recognizable at 16 px.

## Sources

- `frontend/static/brand/mark.svg` is the active full-detail mark used in the header, loading and empty states. `frontend/static/brand/favicon.svg` is its deliberately simplified small-size companion, and `wordmark.svg` supplies the tracked outlined letters used in the header.
- `frontend/src/lib/design/theme.css` is the palette token source (`--brand-primary`, `--brand-paper`, `--brand-surface`, `--brand-ink`, and `--brand-accent`). Existing UI tokens alias these values. Keep them aligned with the SVG colors.
- `docs/assets/brand/` keeps the three original concepts and both pen-panel concepts. All include light and dark lockups, light and dark marks, and a one-color mark. The two pen-panel concepts also include app-icon tiles and simplified favicons.

The active PNG app icons are raster derivatives of `frontend/static/brand/app-icon.svg`; the manifest uses the 192 px and 512 px copies. The PDF cover uses vector paths so the cover remains portable and independent of web assets.

## Pen-panel directions

- **Crossing Pen** places the pen in front of the panel frame. It is the shipped recommendation because the diagonal body and pointed nib remain distinct at small sizes while the frame is still visible.
- **Drawing Ink** puts the nib at the panel edge and lets a watercolor stroke leave the frame. It emphasizes the act of drawing, but the extra stroke is less clear at favicon size.

The comparison lockups use hand-built outlined letter paths. Their shared horizontal tracking was increased by about 6% to separate the initial `i` and `c` without changing the letter shapes.

## Swapping concepts

Choose a concept in `docs/assets/brand/`, then update the active `mark.svg`, `favicon.svg`, `app-icon.svg`, and `wordmark.svg` sources in `frontend/static/brand/`. Keep the SVG colors synchronized with `theme.css`, regenerate the PNG icon fallbacks and preview sheet, and update the PDF cover mark in `ai/app/services/pdf.py`. The original three concepts and both pen-panel options remain available as alternates.
