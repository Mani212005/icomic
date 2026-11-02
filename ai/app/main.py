"""FastAPI entry point for Icomic Python AI service."""

from typing import Any

from fastapi import FastAPI, HTTPException, Request
from fastapi.exceptions import RequestValidationError
from fastapi.responses import JSONResponse

from .adapters.factory import AdapterSet, build_adapters_from_env
from .gen.models import (
    AnalyzeScriptRequest,
    AnalyzeScriptResponse,
    AssemblePdfRequest,
    AssemblePdfResponse,
    ErrorResponse,
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
from .policies import PermissiveGeoPolicy
from .services.character import CharacterService
from .services.colour import ColourService
from .services.draft import DraftService
from .services.line_art import LineArtService
from .services.moderation import ModerationService
from .services.panel import PanelService
from .services.pdf import PdfAssemblyService
from .services.script import ScriptService


def create_app(adapters: AdapterSet | None = None) -> FastAPI:
    """Create and configure the FastAPI application with injected adapters."""
    app = FastAPI(
        title="Icomic AI Service Internal API",
        version="1.0.0",
        description="Internal HTTP contract between Go API and Python AI service.",
    )

    wired_adapters = adapters or build_adapters_from_env()

    app.state.adapters = wired_adapters
    app.state.language_adapter = wired_adapters.language
    app.state.vision_adapter = wired_adapters.vision
    app.state.image_adapter = wired_adapters.image
    app.state.storage_adapter = wired_adapters.storage
    app.state.queue_adapter = wired_adapters.queue
    app.state.clock_adapter = wired_adapters.clock

    # Wire business logic services with injected adapter ports
    script_service = ScriptService(wired_adapters.language)
    character_service = CharacterService(wired_adapters.image, wired_adapters.storage)
    panel_service = PanelService(wired_adapters.language)
    draft_service = DraftService(wired_adapters.image, wired_adapters.storage)
    line_art_service = LineArtService(wired_adapters.image, wired_adapters.storage)
    colour_service = ColourService(wired_adapters.vision)
    moderation_service = ModerationService(wired_adapters.language)
    pdf_service = PdfAssemblyService(wired_adapters.storage)

    # In-memory job status map for development and test
    job_statuses: dict[str, dict[str, Any]] = {}
    app.state.job_statuses = job_statuses

    # Pluggable policy seam (safe defaults)
    app.state.geo_policy = PermissiveGeoPolicy()

    # Exception handlers conforming to ErrorResponse schema
    @app.exception_handler(RequestValidationError)
    async def validation_exception_handler(request: Request, exc: RequestValidationError):
        return JSONResponse(
            status_code=400,
            content=ErrorResponse(
                code="INVALID_ARGUMENT",
                message="Invalid request parameters or body.",
                details={"errors": exc.errors()},
            ).model_dump(),
        )

    @app.exception_handler(ValueError)
    async def value_error_handler(request: Request, exc: ValueError):
        return JSONResponse(
            status_code=400,
            content=ErrorResponse(
                code="INVALID_ARGUMENT",
                message=str(exc),
            ).model_dump(),
        )

    @app.exception_handler(HTTPException)
    async def http_exception_handler(request: Request, exc: HTTPException):
        code = "NOT_FOUND" if exc.status_code == 404 else "ERROR"
        return JSONResponse(
            status_code=exc.status_code,
            content=ErrorResponse(
                code=code,
                message=str(exc.detail),
            ).model_dump(),
        )

    # =========================================================================
    # Routes
    # =========================================================================

    @app.get("/healthz", response_model=HealthResponse)
    def health_check():
        return HealthResponse(status="ok")

    @app.post("/internal/v1/analyze-script", response_model=AnalyzeScriptResponse)
    def analyze_script(req: AnalyzeScriptRequest):
        profiles, chapters, word_count, warnings = script_service.analyze_script(
            req.script, req.target_chapter_count
        )
        job_statuses[req.job_id] = {"status": "done", "actual_cost": 0.0}
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
        job_statuses[req.job_id] = {"status": "done", "actual_cost": 0.0}
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
        job_statuses[req.job_id] = {"status": "done", "actual_cost": 0.0}
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
            line_art_image_bytes=b"dummy-line-art-bytes",
            panel_context=req.panel_context,
        )
        job_statuses[req.job_id] = {"status": "done", "actual_cost": 0.0}
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
        job_statuses[req.job_id] = {"status": "done", "actual_cost": 0.0}
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
        url, preserved, count = pdf_service.assemble_pdf(
            project_id=req.project_id,
            title=req.title,
            page_size=req.page_size,
            include_ai_disclosure=req.include_ai_disclosure,
            pages=req.pages,
        )
        job_statuses[req.job_id] = {"status": "done", "actual_cost": 0.0}
        return AssemblePdfResponse(
            job_id=req.job_id,
            pdf_url=url,
            provenance_metadata_preserved=preserved,
            page_count=count,
        )

    @app.get(
        "/internal/v1/job/{id}/status",
        response_model=JobStatusResponse,
    )
    def get_job_status(id: str):
        if id not in job_statuses:
            raise HTTPException(status_code=404, detail=f"Job '{id}' not found.")
        data = job_statuses[id]
        return JobStatusResponse(
            job_id=id,
            status=data.get("status", "pending"),
            actual_cost=data.get("actual_cost"),
            error_message=data.get("error_message"),
        )

    return app


app = create_app()
