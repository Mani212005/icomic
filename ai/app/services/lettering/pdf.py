"""Draw stored lettering as PDF paths and positioned, subset-embedded glyphs.

No wrapping, shaping, image access or provider calls occur here. Geometry follows
frontend/src/lib/lettering/geometry.js, including the open speech-tail seam and
clipped inward stroke. Fonts are shared across all eligible pages in a document.
"""

import math
import zlib
from hashlib import sha256
from io import BytesIO
from itertools import pairwise

from fontTools import subset
from fontTools.ttLib import TTFont

from ...gen.models import PageLetteringState, PdfLetteringWarning
from . import source_digest
from .typography import FONT_DIRECTORY, FONT_FILES


def eligibility(page):
    """Return the stored layout or a typed, visible publication warning."""
    if page.get("lettering_mode", "baked") != "overlay":
        return None, None
    raw = page.get("lettering")
    state = PageLetteringState.model_validate(raw) if raw else None
    stale = page.get("lettering_stale", False) or state is None
    if state:
        effective = state.input.model_copy(deep=True)
        for panel in effective.panels:
            if panel.panel_id in state.zone_overrides:
                panel.reserved_zones = state.zone_overrides[panel.panel_id]
            for utterance in panel.utterances:
                utterance.placement = state.overrides.get(utterance.utterance_id)
        stale = stale or effective.art_mode != "text_free"
        stale = stale or state.layout.source_digest != source_digest(effective)
        stale = stale or state.layout.page_id != effective.page_id
    if stale:
        code, message, flags = (
            "stale_lettering",
            "Lettering is stale or missing. Recompute and review before publishing.",
            [],
        )
    else:
        flags = sorted(
            {i.flag.root for i in state.layout.issues}
            | {f.root for b in state.layout.bubbles for f in b.flags}
        )
        if not flags:
            return state.layout, None
        code, message = (
            "lettering_review_required",
            "Lettering needs review: " + ", ".join(flags) + ". Fix before publishing.",
        )
    return None, PdfLetteringWarning(
        page_number=page["page_number"], code=code, message=message, flags=flags
    )


def number(value):
    return f"{value:.8f}".rstrip("0").rstrip(".") or "0"


def point(x, y):
    return f"{number(x)} {number(y)}"


def arc(cx, cy, rx, ry, start, sweep):
    """Cubic ellipse arcs, at most a quarter turn each (PDF has no arc operator)."""
    steps = max(1, math.ceil(abs(sweep) / (math.pi / 2)))
    result = []
    for i in range(steps):
        a, b = start + sweep * i / steps, start + sweep * (i + 1) / steps
        k = 4 / 3 * math.tan((b - a) / 4)
        result.append(
            " ".join(
                [
                    point(
                        cx + rx * (math.cos(a) - k * math.sin(a)),
                        cy + ry * (math.sin(a) + k * math.cos(a)),
                    ),
                    point(
                        cx + rx * (math.cos(b) + k * math.sin(b)),
                        cy + ry * (math.sin(b) - k * math.cos(b)),
                    ),
                    point(cx + rx * math.cos(b), cy + ry * math.sin(b)),
                    "c",
                ]
            )
        )
    return result


def shape_path(shape, aspect):
    b = shape.bounds
    x, y, w, h = b.x * aspect, b.y, b.width * aspect, b.height
    if shape.kind == "ellipse":
        return [
            point(x + w, y + h / 2) + " m",
            *arc(x + w / 2, y + h / 2, w / 2, h / 2, 0, math.tau),
            "h",
        ]
    r = min(shape.radius, w / 2, h / 2)
    return [
        point(x + r, y) + " m",
        point(x + w - r, y) + " l",
        *arc(x + w - r, y + r, r, r, -math.pi / 2, math.pi / 2),
        point(x + w, y + h - r) + " l",
        *arc(x + w - r, y + h - r, r, r, 0, math.pi / 2),
        point(x + r, y + h) + " l",
        *arc(x + r, y + h - r, r, r, math.pi / 2, math.pi / 2),
        point(x, y + r) + " l",
        *arc(x + r, y + r, r, r, math.pi, math.pi / 2),
        "h",
    ]


def bubble_path(bubble, aspect):
    if not bubble.tail or bubble.shape.kind != "ellipse":
        return shape_path(bubble.shape, aspect)
    b, a, z = bubble.shape.bounds, bubble.tail[0], bubble.tail[-1]
    cx, cy, rx, ry = b.x + b.width / 2, b.y + b.height / 2, b.width / 2, b.height / 2
    start = math.atan2((a.y - cy) / ry, (a.x - cx) / rx)
    end = math.atan2((z.y - cy) / ry, (z.x - cx) / rx)
    delta = (end - start) % math.tau
    sweep = math.tau - delta if delta < math.pi else -delta
    return (
        [point(p.x * aspect, p.y) + (" m" if i == 0 else " l") for i, p in enumerate(bubble.tail)]
        + arc(cx * aspect, cy, rx * aspect, ry, end, sweep)
        + ["h"]
    )


def line_codes(line):
    # One CID per (glyph, cluster text), since a glyph can encode several source
    # sequences. ActualText additionally preserves logical order for Indic marks.
    clusters = sorted({g.cluster for g in line.glyphs} | {len(line.text)})
    ends = dict(pairwise(clusters))
    seen = set()
    for glyph in line.glyphs:
        text = line.text[glyph.cluster : ends[glyph.cluster]] if glyph.cluster not in seen else ""
        seen.add(glyph.cluster)
        yield glyph, (glyph.glyph_id, text)


class PdfLettering:
    def __init__(self, add_object, layouts):
        self.fonts = {}
        used = {}
        for layout in layouts:
            for bubble in layout.bubbles:
                for line in bubble.lines:
                    keys = used.setdefault(line.font_id, {})
                    for _, key in line_codes(line):
                        keys.setdefault(key, len(keys) + 1)
        for index, (font_id, keys) in enumerate(sorted(used.items())):
            self.fonts[font_id] = (f"L{index}", self.embed(add_object, font_id, keys), keys)

    @property
    def resources(self):
        return " ".join(f"/{name} {ref} 0 R" for name, ref, _ in self.fonts.values())

    @staticmethod
    def embed(add, font_id, keys):
        def stream(data, extra=""):
            compressed = zlib.compress(data)
            return add(
                f"<< /Length {len(compressed)} /Filter /FlateDecode {extra} >>\nstream\n".encode()
                + compressed
                + b"\nendstream"
            )

        with TTFont(FONT_DIRECTORY / FONT_FILES[font_id], recalcTimestamp=False) as font:
            order = font.getGlyphOrder()
            glyph_names = {gid: order[gid] for gid, _ in keys}
            options = subset.Options()
            options.recalc_timestamp = False
            options.hinting = False
            options.harfbuzz_repacker = False
            sub = subset.Subsetter(options=options)
            sub.populate(glyphs=list(glyph_names.values()))
            sub.subset(font)
            encoded = BytesIO()
            subset.save_font(font, encoded, options)
            data = encoded.getvalue()
            file_id = stream(data, f"/Length1 {len(data)}")
            tag = "".join(chr(65 + c % 26) for c in sha256(data).digest()[:6])
            name = f"{tag}+{font_id}"
            scale = 1000 / font["head"].unitsPerEm
            head = font["head"]
            bbox = " ".join(number(v * scale) for v in (head.xMin, head.yMin, head.xMax, head.yMax))
            desc = add(
                f"<< /Type /FontDescriptor /FontName /{name} /Flags 4 /FontBBox [{bbox}] /ItalicAngle 0 /Ascent {number(font['hhea'].ascent * scale)} /Descent {number(font['hhea'].descent * scale)} /CapHeight {number(font['hhea'].ascent * scale)} /StemV 80 /FontFile2 {file_id} 0 R >>".encode()
            )
            gids = bytearray((len(keys) + 1) * 2)
            widths, mappings = [], []
            for (gid, text), cid in keys.items():
                glyph = glyph_names[gid]
                gids[cid * 2 : cid * 2 + 2] = font.getGlyphID(glyph).to_bytes(2, "big")
                widths.append(f"{cid} [{number(font['hmtx'][glyph][0] * scale)}]")
                if text:
                    mappings.append(f"<{cid:04X}> <{text.encode('utf-16-be').hex().upper()}>")
            map_id = stream(bytes(gids))
            descendant = add(
                f"<< /Type /Font /Subtype /CIDFontType2 /BaseFont /{name} /CIDSystemInfo << /Registry (Adobe) /Ordering (Identity) /Supplement 0 >> /FontDescriptor {desc} 0 R /CIDToGIDMap {map_id} 0 R /W [{' '.join(widths)}] >>".encode()
            )
            cmap = [
                "/CIDInit /ProcSet findresource begin 12 dict begin begincmap",
                "/CIDSystemInfo << /Registry (Adobe) /Ordering (UCS) /Supplement 0 >> def",
                "/CMapName /IcomicUnicode def /CMapType 2 def",
                "1 begincodespacerange <0000> <FFFF> endcodespacerange",
            ]
            for offset in range(0, len(mappings), 100):
                group = mappings[offset : offset + 100]
                cmap.extend([f"{len(group)} beginbfchar", *group, "endbfchar"])
            cmap.append("endcmap CMapName currentdict /CMap defineresource pop end end")
            unicode_id = stream("\n".join(cmap).encode())
            return add(
                f"<< /Type /Font /Subtype /Type0 /BaseFont /{name} /Encoding /Identity-H /DescendantFonts [{descendant} 0 R] /ToUnicode {unicode_id} 0 R >>".encode()
            )

    def draw(self, layout, x, y, width, height):
        aspect = layout.page_aspect
        commands = [
            "q",
            f"{number(width / aspect)} 0 0 {number(-height)} {number(x)} {number(y + height)} cm",
            "1 j",
        ]
        for bubble in layout.bubbles:
            paths = bubble_path(bubble, aspect)
            for shape in bubble.thought_tail:
                paths += shape_path(shape, aspect)
            commands += [
                "q",
                *paths,
                "W n",
                "1 g 0 G",
                f"{number(layout.stroke_width * 2)} w",
                *paths,
                "B",
                "Q",
                "0 g",
            ]
            for line in bubble.lines:
                name, _, keys = self.fonts[line.font_id]
                actual = (b"\xfe\xff" + line.text.encode("utf-16-be")).hex().upper()
                commands += [
                    f"/Span << /ActualText <{actual}> >> BDC",
                    f"BT /{name} {number(line.font_size)} Tf",
                ]
                for glyph, key in line_codes(line):
                    commands.append(
                        f"1 0 0 -1 {point(glyph.x * aspect, glyph.y)} Tm <{keys[key]:04X}> Tj"
                    )
                commands += ["ET", "EMC"]
        return "\n".join([*commands, "Q"]).encode()
