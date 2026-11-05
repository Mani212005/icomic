"""Versioned style bible resolver shared by character reference, draft, final line art, and palette suggestion."""

import hashlib
import json
from dataclasses import asdict, dataclass
from typing import Any

STYLE_BIBLE_VERSION = "style-bible-v1"


@dataclass(frozen=True)
class StylePaletteGuidance:
    theme: str
    dominant_tones: list[str]
    accent_tones: list[str]
    temperature: str
    suggested_regions: dict[str, str]

    def to_dict(self) -> dict[str, Any]:
        return asdict(self)


@dataclass(frozen=True)
class StyleBible:
    style_id: str
    label: str
    line_treatment: str
    environment: str
    materials: str
    values: str
    palette: StylePaletteGuidance
    lighting: str
    mood: str
    depth: str
    negative_constraints: list[str]
    version: str = STYLE_BIBLE_VERSION

    @property
    def bible_hash(self) -> str:
        """Stable SHA-256 hash of the canonical serialized preset."""
        data = self.to_dict(include_hash=False)
        canonical = json.dumps(data, sort_keys=True, ensure_ascii=False)
        return hashlib.sha256(canonical.encode("utf-8")).hexdigest()

    def to_dict(self, include_hash: bool = True) -> dict[str, Any]:
        result: dict[str, Any] = {
            "depth": self.depth,
            "environment": self.environment,
            "label": self.label,
            "lighting": self.lighting,
            "line_treatment": self.line_treatment,
            "materials": self.materials,
            "mood": self.mood,
            "negative_constraints": list(self.negative_constraints),
            "palette": self.palette.to_dict(),
            "style_id": self.style_id,
            "values": self.values,
            "version": self.version,
        }
        if include_hash:
            result["bible_hash"] = self.bible_hash
        return result

    def format_for_character_reference(self, character_name: str, prompt: str) -> str:
        """Character turnaround prompt guidance incorporating style bible parameters."""
        negatives = ", ".join(self.negative_constraints)
        palette_tones = f"Dominant tones ({', '.join(self.palette.dominant_tones)}) with accents ({', '.join(self.palette.accent_tones)})"
        return (
            f"Character reference sheet for {character_name}: {prompt}. "
            f"Full turnaround, clear facial details, front and side view. "
            f"Style bible [{self.style_id} v{self.version}]: "
            f"Line treatment: {self.line_treatment} "
            f"Materials: {self.materials} "
            f"Palette: {palette_tones}, {self.palette.temperature} temperature. "
            f"Lighting: {self.lighting} "
            f"Mood: {self.mood} "
            f"Depth and staging: {self.depth} "
            f"Negative constraints: {negatives}."
        )

    def format_for_draft(self, page_number: int, panel_count: int) -> str:
        """Stage prompt for rough draft composition reflecting style staging."""
        return (
            f"Rough comic book page layout draft sketch, {panel_count}-panel grid composition, "
            "portrait-oriented full comic page, taller than wide, composed to fit the canvas without cropping, "
            f"rough pencil layout for page {page_number}. "
            f"Follow {self.label} staging: {self.depth} Mood: {self.mood}. "
            f"Style bible: {self.style_id} (version {self.version})."
        )

    def format_for_line_art(self, page_number: int, panel_count: int) -> str:
        """Stage prompt for final black-and-white inking adhering to style line and tonal guidance.

        Avoids contradictory blanket exclusions like 'flat white fills, no shading' when the
        style specifies screentones, cross-hatching, or tonal chiaroscuro.
        """
        negatives = ", ".join(self.negative_constraints)
        return (
            f"Comic book page {page_number} final inking and line art, {panel_count}-panel grid. "
            "portrait-oriented full comic page, taller than wide, composed to fit the canvas without cropping. "
            f"Follow the approved draft composition. "
            f"Inking and line treatment: {self.line_treatment} "
            f"Tonal values and contrast: {self.values} "
            f"Lighting in inking: {self.lighting} "
            f"Materials and textures: {self.materials} "
            f"Style bible: {self.style_id} (version {self.version}). "
            f"Negative constraints: {negatives}."
        )

    def format_for_palette_vision(self, panel_context: str) -> str:
        """Enrich vision palette prompt context with style bible palette instructions."""
        palette_info = (
            f"Style: {self.label} ({self.style_id}). "
            f"Theme: {self.palette.theme}. "
            f"Dominant tones: {', '.join(self.palette.dominant_tones)}. "
            f"Accent tones: {', '.join(self.palette.accent_tones)}. "
            f"Color temperature: {self.palette.temperature}. "
            f"Suggested region harmonies: {json.dumps(self.palette.suggested_regions, ensure_ascii=False)}."
        )
        if panel_context.strip():
            return f"{panel_context}. Style palette bible: {palette_info}"
        return f"Style palette bible: {palette_info}"


STYLE_PRESETS: dict[str, StyleBible] = {
    "classic-comic": StyleBible(
        style_id="classic-comic",
        label="Classic Comic",
        line_treatment="Dynamic variable-width black ink contours, expressive brush lines, clean defined silhouettes and bold exterior weights.",
        environment="Newsprint comic texture, graphic halftone dot screens, metropolitan streets and pulp environments.",
        materials="Matte paper tooth, printed ink grain, weathered canvas, cotton fabrics and graphic metals.",
        values="High-contrast black-and-white value hierarchy, punchy ink shadows, balanced negative white space, graphic chiaroscuro without muddy grey gradients.",
        palette=StylePaletteGuidance(
            theme="Four-Color Vintage Print",
            dominant_tones=["deep navy", "warm newsprint white", "ink black"],
            accent_tones=["canary yellow", "crimson red", "cobalt blue"],
            temperature="warm balanced vintage",
            suggested_regions={
                "character_clothing": "#D32F2F",
                "character_skin": "#FFCC80",
                "background_sky": "#1976D2",
                "architecture_shadows": "#1A237E",
                "accent_lighting": "#FFEB3B",
            },
        ),
        lighting="High-contrast key lighting, defined graphic cast shadows, crisp rim light delineating silhouettes.",
        mood="Classic golden-age pulp adventure, optimistic heroism, energetic narrative punch.",
        depth="Three-plane theatrical staging: distinct foreground silhouette framing, focused midground action, graphic background scenery.",
        negative_constraints=[
            "no photorealistic airbrushing",
            "no muddy 3D CGI gradients",
            "no modern lens flare",
            "no unshaded flat vector clip-art",
            "no desaturated washed-out haze",
        ],
    ),
    "cyberpunk-noir": StyleBible(
        style_id="cyberpunk-noir",
        label="Cyberpunk Noir",
        line_treatment="Sharp precision technical ink contours, fine architectural linework, crisp geometric separation and stark silhouette cuts.",
        environment="Rain-slicked asphalt, dense multi-tiered megacity alleys, glowing neon signage, wet concrete reflections and dystopian industrial sprawl.",
        materials="Reflective wet glass, brushed chrome, holographic displays, distressed synthetics, worn leather and dark carbon fiber.",
        values="Stark chiaroscuro contrast, deep obsidian ink pools, illuminated edge highlights cut sharply against heavy atmospheric darks.",
        palette=StylePaletteGuidance(
            theme="Neon Drenched Midnight",
            dominant_tones=["midnight obsidian", "deep cyan-slate", "charcoal"],
            accent_tones=["hot magenta", "electric cyan", "acid yellow"],
            temperature="cool moody night with intense hot neon accents",
            suggested_regions={
                "character_clothing": "#212121",
                "character_skin": "#FFD1A4",
                "background_sky": "#0A0E17",
                "architecture_shadows": "#0D1B2A",
                "accent_lighting": "#00F0FF",
                "neon_glow": "#FF007F",
            },
        ),
        lighting="Low-key atmospheric night illumination, intense multi-point colored neon rim lights, puddle specular reflections, moody silhouette backlighting.",
        mood="Gritty cyber-dystopian tension, investigative noir mystery, rain-drenched urban isolation.",
        depth="Dense vertical layering, atmospheric smog and rain precipitation curtains, illuminated foreground reflections receding into hazy illuminated spires.",
        negative_constraints=[
            "no sunny daylight fills",
            "no pastel countryside tones",
            "no rustic pastoral materials",
            "no generic flat cartoon fills",
            "no soft watercolor bleeds",
        ],
    ),
    "classic-superhero": StyleBible(
        style_id="classic-superhero",
        label="Classic Superhero",
        line_treatment="Muscular dynamic ink contours, athletic anatomical weighting, clean cross-feathering for musculature and power silhouettes.",
        environment="Towering metropolitan skyscrapers, dramatic gargoyles, bridge towers, cosmic skies and urban crisis landscapes.",
        materials="Tactical armor sheen, spandex weave, brushed steel, polished brass, crackling energy particles and stone masonry.",
        values="Dynamic three-dimensional form modeling, deep ink contour shadows, brilliant specular highlights sculpting anatomy.",
        palette=StylePaletteGuidance(
            theme="Heroic Primary Bold",
            dominant_tones=["cobalt blue", "deep ink charcoal", "pure paper white"],
            accent_tones=["crimson red", "brilliant gold", "electric azure"],
            temperature="vibrant bold dynamic",
            suggested_regions={
                "character_clothing": "#0D47A1",
                "character_skin": "#F5CBA7",
                "background_sky": "#1565C0",
                "architecture_shadows": "#263238",
                "accent_lighting": "#FFD600",
                "cape_or_emblem": "#C62828",
            },
        ),
        lighting="Dramatic overhead key illumination, heroic upward fill, striking rim lighting separating figures from explosive background skies.",
        mood="Epic cinematic stakes, heroic determination, kinetic power and moral clarity.",
        depth="Exaggerated wide-angle perspective, dynamic foreground foreshortening, deep receding metropolitan avenues.",
        negative_constraints=[
            "no stick-figure or schematic proportions",
            "no dull desaturated monochrome haze",
            "no muddy flat vector fills",
            "no muted slice-of-life intimacy",
            "no unshaded digital flat shapes",
        ],
    ),
    "manga-ink": StyleBible(
        style_id="manga-ink",
        label="Shonen Manga Ink",
        line_treatment="Expressive G-pen inking, delicate hairline-to-bold dynamic stroke transitions, sharp speedlines, intricate hair and facial detailing.",
        environment="Traditional Japanese manga backdrops, dense speedline bursts, atmospheric screentone skies, crisp interior line art.",
        materials="Traditional sumi ink on Kent paper, mechanical screentone film (Benday halftone dots), cross-hatching, white gouache correction accents.",
        values="Disciplined monochromatic value scale, rich sumi blacks, multi-density grey screentone modeling, dense cross-hatch shadows, pure paper-white highlights.",
        palette=StylePaletteGuidance(
            theme="Monochrome Screentone Ink",
            dominant_tones=["sumi black", "50% midtone grey screentone", "pure paper white"],
            accent_tones=[
                "delicate 20% light grey tone",
                "dense 70% shadow tone",
                "subtle vermilion ink accent",
            ],
            temperature="monochromatic neutral with sumi warmth",
            suggested_regions={
                "character_clothing": "#2B2B2B",
                "character_skin": "#FDFEFE",
                "background_sky": "#7F8C8D",
                "architecture_shadows": "#17202A",
                "accent_lighting": "#FFFFFF",
                "screentone_midtones": "#BDC3C7",
            },
        ),
        lighting="Directional graphic chiaroscuro, screentone form shadows, dramatic rim lighting cut by cross-hatching, high-contrast action bursts.",
        mood="High-octane shonen energy, disciplined martial focus, dramatic kinetic tension.",
        depth="Extreme focal perspective, converging speedline depth cues, sharp foreground ink framing against screentoned distant layers.",
        negative_constraints=[
            "no western-style flat colored fills",
            "no muddy digital airbrush smears",
            "no uniform stick-figure outlines",
            "no color fringing or chromatic aberration",
            "no unhatched blurry grey gradients",
        ],
    ),
    "indie-graphic-novel": StyleBible(
        style_id="indie-graphic-novel",
        label="Indie Graphic Novel",
        line_treatment="Organic textured dip-pen and brush inking, subtle line wobble and human hand idiosyncrasy, delicate cross-hatching.",
        environment="Lived-in intimate domestic interiors, European city streets, overgrown coastal foliage, weathered wooden structures.",
        materials="Heavy rough watercolor paper tooth, matte gouache finishes, worn linen, natural grain wood, ceramic and aged paper textures.",
        values="Gentle midtone modeling, soft shadow boundaries, restrained tonal washes, warm grey transitions rather than harsh graphic cuts.",
        palette=StylePaletteGuidance(
            theme="Muted Earthy Literary",
            dominant_tones=["slate blue", "weathered terracotta", "warm parchment"],
            accent_tones=["moss olive", "dusty rose", "antique ochre"],
            temperature="warm muted desaturated earthy",
            suggested_regions={
                "character_clothing": "#5D6D7E",
                "character_skin": "#EDBB99",
                "background_sky": "#A3B18A",
                "architecture_shadows": "#3E2723",
                "accent_lighting": "#D4AC0D",
                "environment_earth": "#8D6E63",
            },
        ),
        lighting="Soft natural ambient window light, gentle golden-hour diffusion, subtle bounced warmth, delicate soft-edged cast shadows.",
        mood="Introspective, poetic narrative depth, literary graphic novel sensitivity, quiet human observation.",
        depth="Atmospheric tonal perspective, thoughtful room-scale framing, naturalistic spatial staging without artificial distortion.",
        negative_constraints=[
            "no high-gloss synthetic neons",
            "no superhero muscular exaggeration",
            "no rigid vector geometric lines",
            "no harsh digital gradients",
            "no commercial clip-art flat fills",
        ],
    ),
}


def resolve_style_bible(style_id: str | None) -> StyleBible:
    """Resolve a style identifier to a versioned, coherent StyleBible.

    If style_id is unknown or custom, gracefully construct a deterministic
    StyleBible tailored around the raw style identifier with balanced baseline properties.
    """
    if not style_id or not style_id.strip():
        return STYLE_PRESETS["classic-comic"]
    normalized = style_id.strip().lower()
    if normalized in STYLE_PRESETS:
        return STYLE_PRESETS[normalized]

    # Deterministic fallback for custom/unknown styles
    label = " ".join(
        word.capitalize() for word in normalized.replace("-", " ").replace("_", " ").split()
    )
    return StyleBible(
        style_id=normalized,
        label=label,
        line_treatment=f"Clean expressive comic ink lines with dynamic weight variation tailored to {label}.",
        environment=f"Atmospheric comic world surfaces and textural environments aligned with {label}.",
        materials=f"Authentic comic stock texture, material surface differentiation and tactile finish for {label}.",
        values="Clear tonal hierarchy with distinct midtones, deep ink contrast and sculpted volume modeling.",
        palette=StylePaletteGuidance(
            theme=f"{label} Cohesive Palette",
            dominant_tones=["slate charcoal", "neutral deep navy", "warm off-white"],
            accent_tones=[
                "saturated primary accent",
                "secondary warm tone",
                "clean highlight tone",
            ],
            temperature="balanced narrative harmony",
            suggested_regions={
                "character_clothing": "#34495E",
                "character_skin": "#F5CBA7",
                "background_sky": "#2C3E50",
                "architecture_shadows": "#1A252F",
                "accent_lighting": "#F39C12",
            },
        ),
        lighting="Directional key and rim lighting providing sculptural volume, form definition and depth.",
        mood=f"Evocative storytelling atmosphere tailored to {label}.",
        depth="Distinct three-plane depth staging: clear foreground framing, focused midground action, graphic background.",
        negative_constraints=[
            "no generic flat vector blocks",
            "no unshaded stick figures",
            "no muddy undifferentiated tones",
            "no arbitrary digital blur",
            "no flat clip-art cartoon fills",
        ],
    )
