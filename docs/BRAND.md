# Icomic brand assets

The shipped direction is **Painted Bubble**: a hand-painted speech balloon with an ink contour and paper-white lettering. It ties the editor's conversation workflow to the existing watercolor palette and keeps a clear silhouette at small sizes.

## Sources

- `frontend/static/brand/mark.svg` is the single source for the active mark. The header pairs it with the Kalam heading wordmark; loading and empty states and the favicon use the same SVG.
- `frontend/src/lib/design/theme.css` is the palette token source (`--brand-primary`, `--brand-paper`, `--brand-surface`, `--brand-ink`, and `--brand-accent`). Existing UI tokens alias these values. Keep them aligned with the SVG colors.
- `docs/assets/brand/` holds the three comparison concepts, each as a lockup, mark, one-color stamp, and dark-background variants.

The active mark uses the same local SVG for the header, loading and empty states, and SVG favicon. PNG app icon fallbacks are rendered from that source. PDF cover artwork is drawn as PDF vector paths to keep the cover portable and independent of web assets.

## Swapping concepts

Choose a concept in `docs/assets/brand/`, then replace the artwork in `frontend/static/brand/mark.svg`. Keep the SVG colors synchronized with `theme.css`. Regenerate the PNG icon fallbacks and the preview sheet from that mark. Update the small PDF speech mark in `ai/app/services/pdf.py` to match the selected silhouette. The concept comparison files remain intact for reference.
