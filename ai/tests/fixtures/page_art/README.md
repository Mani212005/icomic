# Real page-art fixture

`live_margin_annotations.png` is the metadata-free grayscale reduction to 768 x 512 of the original image extracted from `/home/mani/icomic-artifacts/comic-e2e/live-latest.pdf` (2026-11-04). The original 1920 x 1280 extracted PNG has SHA-256 `14727286fa2dd672012b18603f0de0de1772c245d0526c91d573b4c60eb493bb`. `latest.pdf` contained the same image when inspected.

The fixture contains only the fictional comic page: four panels, a heading and notes about the character. No PDF cover, project/run identifier, user details, or source metadata is included. It is 204,795 bytes. The full panel grid is retained so tests can prove artwork preservation rather than testing a detached text strip.

Reproduction used existing artifacts only: `pdfimages -all` to extract, `pdftoppm -f 2 -l 2` to render, then Pillow grayscale conversion and resize. No live calls were made. See [cleanup thresholds and limitations](../../../../docs/PAGE_ART.md).
