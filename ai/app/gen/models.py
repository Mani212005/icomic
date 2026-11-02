"""Pydantic models matching contracts/openapi/ai-service.yaml."""

from typing import Any

from pydantic import BaseModel, Field


class HealthResponse(BaseModel):
    status: str = "ok"


class ErrorResponse(BaseModel):
    code: str
    message: str
    details: dict[str, Any] | None = None


class CharacterProfile(BaseModel):
    name: str
    description: str
    age_range: str
    appearance: str
    hair_style: str
    clothing: str
    personality: str
    visual_attributes: dict[str, Any] = Field(default_factory=dict)


class AnalyzeScriptRequest(BaseModel):
    job_id: str
    hashed_user_id: str
    project_id: str
    script: str
    target_chapter_count: int | None = None


class AnalyzeScriptResponse(BaseModel):
    job_id: str
    character_profiles: list[CharacterProfile]
    suggested_chapter_count: int
    word_count: int
    warnings: list[str] = Field(default_factory=list)


class GenerateCharacterReferenceRequest(BaseModel):
    job_id: str
    hashed_user_id: str
    project_id: str
    character_id: str
    character_name: str
    prompt: str
    reference_images: list[str] = Field(default_factory=list)
    style: str = "classic-comic"


class GenerateCharacterReferenceResponse(BaseModel):
    job_id: str
    image_url: str
    model_id: str
    provenance_metadata: dict[str, Any] = Field(default_factory=dict)


class ChapterOutlineItem(BaseModel):
    chapter_number: int
    title: str
    summary: str
    important_events: list[str] = Field(default_factory=list)
    character_names: list[str] = Field(default_factory=list)
    ending: str


class GenerateChapterOutlineRequest(BaseModel):
    job_id: str
    hashed_user_id: str
    project_id: str
    script: str
    chapter_count: int
    approved_characters: list[CharacterProfile]


class GenerateChapterOutlineResponse(BaseModel):
    job_id: str
    chapters: list[ChapterOutlineItem]


class PanelTextItem(BaseModel):
    panel_number: int
    scene_description: str
    character_actions: str
    camera_angle: str
    background: str
    dialogue: str
    narration: str
    image_prompt: str


class GeneratePanelTextRequest(BaseModel):
    job_id: str
    hashed_user_id: str
    project_id: str
    chapter_id: str
    chapter_number: int
    chapter_title: str
    chapter_summary: str
    characters: list[str] = Field(default_factory=list)


class GeneratePanelTextResponse(BaseModel):
    job_id: str
    panels: list[PanelTextItem]


class GenerateDraftSketchRequest(BaseModel):
    job_id: str
    hashed_user_id: str
    project_id: str
    page_id: str
    page_number: int
    panels: list[PanelTextItem]
    style: str
    character_reference_image_urls: list[str] = Field(default_factory=list)


class GenerateDraftSketchResponse(BaseModel):
    job_id: str
    image_url: str
    model_id: str
    provenance_metadata: dict[str, Any] = Field(default_factory=dict)


class GenerateLineArtRequest(BaseModel):
    job_id: str
    hashed_user_id: str
    project_id: str
    page_id: str
    page_number: int
    draft_image_url: str
    panels: list[PanelTextItem]
    style: str
    character_reference_image_urls: list[str] = Field(default_factory=list)


class GenerateLineArtResponse(BaseModel):
    job_id: str
    image_url: str
    model_id: str
    provenance_metadata: dict[str, Any] = Field(default_factory=dict)


class ColourRegionSuggestion(BaseModel):
    region_name: str
    hex_color: str = Field(pattern=r"^#[0-9A-Fa-f]{6}$")
    label: str


class SuggestColourPaletteRequest(BaseModel):
    job_id: str
    hashed_user_id: str
    project_id: str
    page_id: str
    line_art_image_url: str
    panel_context: str


class SuggestColourPaletteResponse(BaseModel):
    job_id: str
    theme: str
    palette: list[ColourRegionSuggestion]


class ModeratePromptRequest(BaseModel):
    job_id: str
    hashed_user_id: str
    prompt: str


class ModeratePromptResponse(BaseModel):
    job_id: str
    blocked: bool
    reason: str | None = None


class PageExportItem(BaseModel):
    page_number: int
    image_url: str
    panel_count: int | None = None
    provenance_metadata: dict[str, Any] = Field(default_factory=dict)


class AssemblePdfRequest(BaseModel):
    job_id: str
    hashed_user_id: str
    project_id: str
    title: str
    page_size: str = "A4"
    include_ai_disclosure: bool = True
    pages: list[PageExportItem]


class AssemblePdfResponse(BaseModel):
    job_id: str
    pdf_url: str
    provenance_metadata_preserved: bool
    page_count: int


class JobStatusResponse(BaseModel):
    job_id: str
    status: str
    actual_cost: float | None = None
    error_message: str | None = None
