"""FastAPI entry point for Icomic Python AI service."""

import os

from fastapi import FastAPI

from .adapters.clock.fake import FakeClockAdapter
from .adapters.clock.system import SystemClockAdapter
from .adapters.image.fake import FakeImageAdapter
from .adapters.image.meta import MetaImageAdapter
from .adapters.language.fake import FakeLanguageAdapter
from .adapters.language.meta import MetaLanguageAdapter
from .adapters.queue.fake import FakeQueueAdapter
from .adapters.queue.redis import RedisQueueAdapter
from .adapters.storage.fake import FakeStorageAdapter
from .adapters.storage.s3 import S3Adapter
from .adapters.vision.fake import FakeVisionAdapter
from .adapters.vision.meta import MetaVisionAdapter
from .gen.models import (
    AnalyzeScriptRequest,
    AnalyzeScriptResponse,
    AssemblePdfRequest,
    AssemblePdfResponse,
    GenerateChapterOutlineRequest,
    GenerateChapterOutlineResponse,
    GenerateCharacterReferenceRequest,
    GenerateCharacterReferenceResponse,
    GenerateDraftSketchRequest,
    GenerateDraftSketchResponse,
    GenerateLineArtRequest,
    GenerateLineArtResponse,
    GeneratePanelTextRequest,
    GeneratePanelTextResponse,
    HealthResponse,
    JobStatusResponse,
    ModeratePromptRequest,
    ModeratePromptResponse,
    SuggestColourPaletteRequest,
    SuggestColourPaletteResponse,
)
from .services.character import CharacterService
from .services.colour import ColourService
from .services.draft import DraftService
from .services.line_art import LineArtService
from .services.moderation import ModerationService
from .services.panel import PanelService
from .services.script import ScriptService


def create_app() -> FastAPI:
    app = FastAPI(
        title="Icomic AI Service Internal API",
        version="1.0.0",
        description="Internal HTTP contract between Go API and Python AI service.",
    )

    # Wire adapters based on environment config (fake is default in dev and test)
    adapter_lang = os.getenv("ADAPTER_LANGUAGE", "fake").lower()
    adapter_vis = os.getenv("ADAPTER_VISION", "fake").lower()
    adapter_img = os.getenv("ADAPTER_IMAGE", "fake").lower()
    adapter_store = os.getenv("ADAPTER_STORAGE", "fake").lower()
    adapter_q = os.getenv("ADAPTER_QUEUE", "fake").lower()
    adapter_clk = os.getenv("ADAPTER_CLOCK", "fake").lower()

    language_adapter = FakeLanguageAdapter() if adapter_lang == "fake" else MetaLanguageAdapter()
    vision_adapter = FakeVisionAdapter() if adapter_vis == "fake" else MetaVisionAdapter()
    image_adapter = FakeImageAdapter() if adapter_img == "fake" else MetaImageAdapter()
    storage_adapter = FakeStorageAdapter() if adapter_store == "fake" else S3Adapter()
    queue_adapter = FakeQueueAdapter() if adapter_q == "fake" else RedisQueueAdapter()
    clock_adapter = FakeClockAdapter() if adapter_clk == "fake" else SystemClockAdapter()

    app.state.queue_adapter = queue_adapter
    app.state.clock_adapter = clock_adapter
    app.state.storage_adapter = storage_adapter

    # Wire services with injected adapter ports
    script_service = ScriptService(language_adapter)
    character_service = CharacterService(image_adapter, storage_adapter)
    panel_service = PanelService(language_adapter)
    draft_service = DraftService(image_adapter, storage_adapter)
    line_art_service = LineArtService(image_adapter, storage_adapter)
    colour_service = ColourService(vision_adapter)
    moderation_service = ModerationService(language_adapter)

    # In-memory job status map for development and test
    job_statuses: dict[str, dict] = {}

    @app.get("/healthz", response_model=HealthResponse)
    def health_check():
        return HealthResponse(status="ok")

    @app.post("/internal/v1/analyze-script", response_model=AnalyzeScriptResponse)
    def analyze_script(req: AnalyzeScriptRequest):
        profiles, chapters, word_count, warnings = script_service.analyze_script(
            req.script, req.target_chapter_count
        )
        return AnalyzeScriptResponse(
            job_id=req.job_id,
            character_profiles=profiles,
            suggested_chapter_count=chapters,
            word_count=word_count,
            warnings=warnings,
        )

    @app.post(
        "/internal/v1/generate-character-reference",
        response_model=GenerateCharacterReferenceResponse,
    )
    def generate_character_reference(req: GenerateCharacterReferenceRequest):
        url, model_id, provenance = character_service.generate_reference_image(
            character_id=req.character_id,
            character_name=req.character_name,
            prompt=req.prompt,
            hashed_user_id=req.hashed_user_id,
            style=req.style,
        )
        job_statuses[req.job_id] = {"status": "done", "actual_cost": 0.01}
        return GenerateCharacterReferenceResponse(
            job_id=req.job_id,
            image_url=url,
            model_id=model_id,
            provenance_metadata=provenance,
        )

    @app.post(
        "/internal/v1/generate-chapter-outline",
        response_model=GenerateChapterOutlineResponse,
    )
    def generate_chapter_outline(req: GenerateChapterOutlineRequest):
        chapters = panel_service.generate_chapter_outline(
            script=req.script,
            chapter_count=req.chapter_count,
            approved_characters=req.approved_characters,
        )
        return GenerateChapterOutlineResponse(
            job_id=req.job_id,
            chapters=chapters,
        )

    @app.post(
        "/internal/v1/generate-panel-text",
        response_model=GeneratePanelTextResponse,
    )
    def generate_panel_text(req: GeneratePanelTextRequest):
        panels = panel_service.generate_panel_text(
            chapter_number=req.chapter_number,
            chapter_title=req.chapter_title,
            chapter_summary=req.chapter_summary,
            characters=req.characters,
        )
        return GeneratePanelTextResponse(
            job_id=req.job_id,
            panels=panels,
        )

    @app.post(
        "/internal/v1/generate-draft-sketch",
        response_model=GenerateDraftSketchResponse,
    )
    def generate_draft_sketch(req: GenerateDraftSketchRequest):
        url, model_id, provenance = draft_service.generate_draft_sketch(
            page_id=req.page_id,
            page_number=req.page_number,
            panels=req.panels,
            style=req.style,
            hashed_user_id=req.hashed_user_id,
            character_reference_image_urls=req.character_reference_image_urls,
        )
        job_statuses[req.job_id] = {"status": "done", "actual_cost": 0.01}
        return GenerateDraftSketchResponse(
            job_id=req.job_id,
            image_url=url,
            model_id=model_id,
            provenance_metadata=provenance,
        )

    @app.post(
        "/internal/v1/generate-line-art",
        response_model=GenerateLineArtResponse,
    )
    def generate_line_art(req: GenerateLineArtRequest):
        url, model_id, provenance = line_art_service.generate_line_art(
            page_id=req.page_id,
            page_number=req.page_number,
            draft_image_url=req.draft_image_url,
            panels=req.panels,
            style=req.style,
            hashed_user_id=req.hashed_user_id,
            character_reference_image_urls=req.character_reference_image_urls,
        )
        job_statuses[req.job_id] = {"status": "done", "actual_cost": 0.01}
        return GenerateLineArtResponse(
            job_id=req.job_id,
            image_url=url,
            model_id=model_id,
            provenance_metadata=provenance,
        )

    @app.post(
        "/internal/v1/suggest-colour-palette",
        response_model=SuggestColourPaletteResponse,
    )
    def suggest_colour_palette(req: SuggestColourPaletteRequest):
        theme, palette = colour_service.suggest_palette(
            line_art_image_bytes=b"dummy-line-art",
            panel_context=req.panel_context,
        )
        return SuggestColourPaletteResponse(
            job_id=req.job_id,
            theme=theme,
            palette=palette,
        )

    @app.post(
        "/internal/v1/moderate-prompt",
        response_model=ModeratePromptResponse,
    )
    def moderate_prompt(req: ModeratePromptRequest):
        blocked, reason = moderation_service.moderate_prompt(req.prompt)
        return ModeratePromptResponse(
            job_id=req.job_id,
            blocked=blocked,
            reason=reason,
        )

    @app.post(
        "/internal/v1/assemble-pdf",
        response_model=AssemblePdfResponse,
    )
    def assemble_pdf(req: AssemblePdfRequest):
        pdf_bytes = b"%PDF-1.4 mock assembled comic"
        key = f"exports/{req.project_id}/comic.pdf"
        pdf_url = storage_adapter.put(key, pdf_bytes, "application/pdf")
        return AssemblePdfResponse(
            job_id=req.job_id,
            pdf_url=pdf_url,
            provenance_metadata_preserved=True,
            page_count=len(req.pages),
        )

    @app.get(
        "/internal/v1/job/{id}/status",
        response_model=JobStatusResponse,
    )
    def get_job_status(id: str):
        if id not in job_statuses:
            return JobStatusResponse(job_id=id, status="pending")
        data = job_statuses[id]
        return JobStatusResponse(
            job_id=id,
            status=data.get("status", "pending"),
            actual_cost=data.get("actual_cost"),
            error_message=data.get("error_message"),
        )

    return app


app = create_app()
