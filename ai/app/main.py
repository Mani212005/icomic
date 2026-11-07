"""FastAPI entry point for Icomic Python AI service."""

import base64
import binascii
import hashlib
import math
import mimetypes
import os
import posixpath
import uuid
from contextlib import asynccontextmanager
from functools import wraps
from io import BytesIO

from fastapi import FastAPI, HTTPException, Request, Response
from fastapi.exceptions import RequestValidationError
from fastapi.responses import JSONResponse
from PIL import Image

from .adapters.factory import AdapterSet, build_adapters_from_env, get_media_fetch_adapter
from .costs import actual_stage_cost, cost_from_usage
from .gen.models import (
    AnalyzeScriptRequest,
    AnalyzeScriptResponse,
    AssemblePdfRequest,
    AssemblePdfResponse,
    ErrorResponse,
    FinishPageImageRequest,
    FinishPageImageResponse,
    GenerateChapterOutlineRequest,
    GenerateChapterOutlineResponse,
    GenerateCharacterReferenceRequest,
    GenerateCharacterReferenceResponse,
    GenerateDraftSketchRequest,
    GenerateDraftSketchResponse,
    GenerateLineArtRequest,
    GenerateLineArtResponse,
    GeneratePaintedPageRequest,
    GeneratePaintedPageResponse,
    GeneratePanelTextRequest,
    GeneratePanelTextResponse,
    HealthResponse,
    JobStatusResponse,
    LetteringLayout,
    LetteringLayoutRequest,
    ModeratePromptRequest,
    ModeratePromptResponse,
    StoreColourImageRequest,
    StoreColourImageResponse,
    SuggestColourPaletteRequest,
    SuggestColourPaletteResponse,
)
from .policies import DirectStoragePolicy
from .provider_costs import provider_operation
from .provider_diagnostics import last_response
from .services.character import CharacterService
from .services.colour import ColourService
from .services.draft import DraftService
from .services.errors import JobExecutionError, classify_error
from .services.finish_layer import FinishStrength, finish_image, page_seed
from .services.lettering import layout_page
from .services.line_art import LineArtService
from .services.moderation import ModerationService
from .services.painted_page import PaintedPageService
from .services.panel import PanelService
from .services.pdf import PdfAssemblyService
from .services.script import ScriptService
from .services.worker import QUEUE, GenerationWorker


def create_app(adapters: AdapterSet | None = None) -> FastAPI:
    """Create and configure the FastAPI application with injected adapters."""

    @asynccontextmanager
    async def lifespan(app: FastAPI):
        worker.start()
        try:
            yield
        finally:
            worker.close()

    app = FastAPI(
        lifespan=lifespan,
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
    app.state.media_fetch_adapter = wired_adapters.media_fetch or get_media_fetch_adapter(
        wired_adapters.storage
    )
    app.state.queue_adapter = wired_adapters.queue
    app.state.clock_adapter = wired_adapters.clock

    # Wire business logic services with injected adapter ports
    script_service = ScriptService(wired_adapters.language)
    character_service = CharacterService(wired_adapters.image, wired_adapters.storage)
    panel_service = PanelService(wired_adapters.language)
    draft_service = DraftService(wired_adapters.image, wired_adapters.storage)
    line_art_service = LineArtService(wired_adapters.image, wired_adapters.storage)
    painted_page_service = PaintedPageService(wired_adapters.image, wired_adapters.storage)
    colour_service = ColourService(wired_adapters.vision)
    moderation_service = ModerationService(wired_adapters.language)
    pdf_service = PdfAssemblyService(wired_adapters.storage)

    worker = GenerationWorker(wired_adapters.queue)
    app.state.worker = worker

    def queued(operation: str, adapter=None):
        """Keep the HTTP result contract while the consumer owns service execution."""

        def decorate(handler):
            request_type = handler.__annotations__["req"]

            def execute(payload):
                def observed_cost(fallback):
                    ledger = wired_adapters.cost_ledger
                    if ledger is None:
                        return fallback
                    return sum(
                        call["actual_cost_usd"] or 0.0
                        for call in ledger.report()["calls"]
                        if call["job_id"] == str(payload["job_id"])
                    )

                if adapter is not None and hasattr(adapter, "last_usage"):
                    adapter.last_usage = None
                token = provider_operation.set((handler.__name__, str(payload["job_id"])))
                diagnostic_token = last_response.set(None)
                try:
                    result = handler(request_type.model_validate(payload))
                except Exception as exc:
                    usage = getattr(adapter, "last_usage", None)
                    if wired_adapters.cost_ledger is not None or usage is not None:
                        exc.actual_cost = observed_cost(
                            cost_from_usage(operation, usage) if usage is not None else 0.0
                        )
                    if wired_adapters.diagnostics is not None:
                        wired_adapters.diagnostics.failure(type(exc).__name__)
                    raise
                finally:
                    last_response.reset(diagnostic_token)
                    provider_operation.reset(token)
                cost = 0.0
                if adapter is not None:
                    cost = actual_stage_cost(operation, adapter, getattr(result, "model_id", None))
                return result.model_dump(mode="json"), observed_cost(cost)

            worker.register(handler.__name__, execute)

            @wraps(handler)
            def submit(req):
                return worker.submit(handler.__name__, req)

            return submit

        return decorate

    # Pluggable image hosting policy seam
    app.state.image_hosting_policy = DirectStoragePolicy()

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
        structured = exc.error if isinstance(exc, JobExecutionError) else classify_error(exc)
        headers = {}
        if structured.retry_after_seconds is not None:
            headers["Retry-After"] = str(max(1, math.ceil(structured.retry_after_seconds)))
        return JSONResponse(
            status_code=structured.status,
            content=ErrorResponse(
                code=structured.code,
                message=structured.message,
                retryable=structured.retryable,
                retry_after_seconds=structured.retry_after_seconds,
            ).model_dump(),
            headers=headers,
        )

    # =========================================================================
    # Routes
    # =========================================================================

    @app.get("/healthz", response_model=HealthResponse)
    def health_check():
        return HealthResponse(status="ok")

    @app.post("/internal/v1/analyze-script", response_model=AnalyzeScriptResponse)
    @queued("language", wired_adapters.language)
    def analyze_script(req: AnalyzeScriptRequest):
        profiles, chapters, word_count, warnings = script_service.analyze_script(
            req.script, req.target_chapter_count
        )
        return AnalyzeScriptResponse(
            job_id=str(req.job_id),
            character_profiles=profiles,
            suggested_chapter_count=chapters,
            word_count=word_count,
            warnings=warnings,
        )

    @app.post(
        "/internal/v1/generate-character-reference",
        response_model=GenerateCharacterReferenceResponse,
    )
    @queued("image", wired_adapters.image)
    def generate_character_reference(req: GenerateCharacterReferenceRequest):
        url, model_id, provenance = character_service.generate_reference_image(
            character_id=str(req.character_id),
            character_name=req.character_name,
            prompt=req.prompt,
            hashed_user_id=req.hashed_user_id,
            style=req.style,
        )
        return GenerateCharacterReferenceResponse(
            job_id=str(req.job_id),
            image_url=url,
            model_id=model_id,
            provenance_metadata=provenance,
        )

    @app.post(
        "/internal/v1/generate-chapter-outline",
        response_model=GenerateChapterOutlineResponse,
    )
    @queued("language", wired_adapters.language)
    def generate_chapter_outline(req: GenerateChapterOutlineRequest):
        chapters = panel_service.generate_chapter_outline(
            script=req.script,
            chapter_count=req.chapter_count,
            approved_characters=req.approved_characters,
        )
        return GenerateChapterOutlineResponse(
            job_id=str(req.job_id),
            chapters=chapters,
        )

    @app.post(
        "/internal/v1/generate-panel-text",
        response_model=GeneratePanelTextResponse,
    )
    @queued("language", wired_adapters.language)
    def generate_panel_text(req: GeneratePanelTextRequest):
        panels = panel_service.generate_panel_text(
            chapter_number=req.chapter_number,
            chapter_title=req.chapter_title,
            chapter_summary=req.chapter_summary,
            characters=req.characters,
            panel_count=req.panel_count,
            chapter_script=req.chapter_script,
            overlay_lettering=os.getenv("LETTERING_OVERLAY_ENABLED", "false").lower() == "true",
        )
        return GeneratePanelTextResponse(
            job_id=str(req.job_id),
            panels=panels,
        )

    @app.post("/internal/v1/layout-lettering", response_model=LetteringLayout)
    def layout_lettering(req: LetteringLayoutRequest):
        try:
            art_image = wired_adapters.storage.get(req.art_image_url) if req.art_image_url else None
            return layout_page(req.document, art_image=art_image)
        except KeyError as exc:
            raise HTTPException(
                status_code=422, detail="Stored lettering art was not found"
            ) from exc
        except ValueError as exc:
            raise HTTPException(status_code=422, detail=str(exc)) from exc

    @app.post(
        "/internal/v1/generate-draft-sketch",
        response_model=GenerateDraftSketchResponse,
    )
    @queued("image", wired_adapters.image)
    def generate_draft_sketch(req: GenerateDraftSketchRequest):
        url, model_id, provenance = draft_service.generate_draft_sketch(
            page_id=str(req.page_id),
            page_number=req.page_number,
            lettering_document=req.lettering_document,
            panels=req.panels,
            style=req.style,
            hashed_user_id=req.hashed_user_id,
            character_reference_image_urls=req.character_reference_image_urls,
            character_identities=req.character_identities,
        )
        return GenerateDraftSketchResponse(
            job_id=str(req.job_id),
            image_url=url,
            model_id=model_id,
            provenance_metadata=provenance,
        )

    @app.post(
        "/internal/v1/generate-line-art",
        response_model=GenerateLineArtResponse,
    )
    @queued("image", wired_adapters.image)
    def generate_line_art(req: GenerateLineArtRequest):
        url, model_id, provenance = line_art_service.generate_line_art(
            page_id=str(req.page_id),
            page_number=req.page_number,
            draft_image_url=req.draft_image_url,
            panels=req.panels,
            style=req.style,
            hashed_user_id=req.hashed_user_id,
            character_reference_image_urls=req.character_reference_image_urls,
            character_identities=req.character_identities,
        )
        return GenerateLineArtResponse(
            job_id=str(req.job_id),
            image_url=url,
            model_id=model_id,
            provenance_metadata=provenance,
        )

    @app.post(
        "/internal/v1/generate-painted-page",
        response_model=GeneratePaintedPageResponse,
    )
    @queued("image", wired_adapters.image)
    def generate_painted_page(req: GeneratePaintedPageRequest):
        url, model_id, provenance, variant = painted_page_service.generate_painted_page(
            page_id=str(req.page_id),
            page_number=req.page_number,
            lettering_document=req.lettering_document,
            panels=req.panels,
            style=req.style,
            hashed_user_id=req.hashed_user_id,
            variant=req.variant,
            draft_image_url=req.draft_image_url,
            character_reference_image_urls=req.character_reference_image_urls,
            character_identities=req.character_identities,
            style_reference_image_url=req.style_reference_image_url,
        )
        return GeneratePaintedPageResponse(
            job_id=str(req.job_id),
            image_url=url,
            model_id=model_id,
            variant=variant,
            provenance_metadata=provenance,
        )

    @app.post(
        "/internal/v1/suggest-colour-palette",
        response_model=SuggestColourPaletteResponse,
    )
    @queued("vision", wired_adapters.vision)
    def suggest_colour_palette(req: SuggestColourPaletteRequest):
        line_art_image_bytes = app.state.media_fetch_adapter.fetch(req.line_art_image_url)
        theme, palette = colour_service.suggest_palette(
            line_art_image_bytes=line_art_image_bytes,
            panel_context=req.panel_context,
            style=req.style,
        )
        return SuggestColourPaletteResponse(
            job_id=str(req.job_id),
            theme=theme,
            palette=palette,
        )

    @app.post(
        "/internal/v1/store-colour-image",
        response_model=StoreColourImageResponse,
    )
    def store_colour_image(req: StoreColourImageRequest):
        try:
            image_bytes = base64.b64decode(req.image_base64, validate=True)
        except (binascii.Error, ValueError) as exc:
            raise HTTPException(
                status_code=400, detail="Coloured page image is not valid base64"
            ) from exc
        if not image_bytes or len(image_bytes) > 10 * 1024 * 1024:
            raise HTTPException(status_code=400, detail="Coloured page image has an invalid size")
        detected = None
        try:
            with Image.open(BytesIO(image_bytes)) as image:
                detected = image.format
                image.verify()
        except Exception as exc:
            raise HTTPException(
                status_code=400, detail="Coloured page image is not a valid PNG or WebP"
            ) from exc
        if detected == "PNG":
            media_type, extension = "image/png", "png"
        elif detected == "WEBP":
            media_type, extension = "image/webp", "webp"
        else:
            raise HTTPException(status_code=400, detail="Coloured page image must be PNG or WebP")
        if media_type != req.media_type:
            raise HTTPException(
                status_code=400, detail="Coloured page image media type does not match its bytes"
            )
        key = f"pages/{req.page_id}/colours/{uuid.uuid4()}.{extension}"
        image_url = wired_adapters.storage.put(key, image_bytes, media_type)
        return StoreColourImageResponse(image_url=image_url)

    @app.post(
        "/internal/v1/finish-page-image",
        response_model=FinishPageImageResponse,
    )
    def finish_page_image(req: FinishPageImageRequest):
        strength = {
            "print": FinishStrength.PRINT,
            "print-plus": FinishStrength.PLUS,
            "print-heavy": FinishStrength.HEAVY,
        }.get(req.finish_preset.root)
        if strength is None:
            raise HTTPException(status_code=400, detail="Finish preset must not be off")
        source = app.state.media_fetch_adapter.fetch(req.image_url)
        finished = finish_image(
            source,
            seed=page_seed(f"{req.page_id}/{req.finish_preset.root}"),
            strength=strength,
        )
        with Image.open(BytesIO(finished)) as image:
            output = BytesIO()
            image.convert("RGB").save(output, format="JPEG", quality=92, optimize=True)
        display_bytes = output.getvalue()
        digest = hashlib.sha256(display_bytes).hexdigest()
        key = f"finishes/{req.page_id}/{req.finish_preset.root}/{digest}.jpg"
        return FinishPageImageResponse(
            image_url=app.state.storage_adapter.put(key, display_bytes, "image/jpeg")
        )

    @app.post(
        "/internal/v1/moderate-prompt",
        response_model=ModeratePromptResponse,
    )
    @queued("language", wired_adapters.language)
    def moderate_prompt(req: ModeratePromptRequest):
        blocked, reason = moderation_service.moderate_prompt(req.prompt)
        return ModeratePromptResponse(
            job_id=str(req.job_id),
            blocked=blocked,
            reason=reason,
        )

    @app.post(
        "/internal/v1/assemble-pdf",
        response_model=AssemblePdfResponse,
    )
    @queued("pdf")
    def assemble_pdf(req: AssemblePdfRequest):
        url, preserved, count, warnings = pdf_service.assemble_pdf(
            project_id=str(req.project_id),
            title=req.title,
            page_size=req.page_size,
            include_ai_disclosure=req.include_ai_disclosure,
            pages=req.pages,
            finish_preset=req.finish_preset.root if req.finish_preset else "off",
            finish_preset_explicit=req.finish_preset_explicit,
        )
        return AssemblePdfResponse(
            job_id=str(req.job_id),
            pdf_url=url,
            provenance_metadata_preserved=preserved,
            page_count=count,
            warnings=warnings,
        )

    @app.get(
        "/internal/v1/job/{id}/status",
        response_model=JobStatusResponse,
    )
    def get_job_status(id: str):
        data = wired_adapters.queue.get_job(QUEUE, id)
        if data is None:
            raise HTTPException(status_code=404, detail=f"Job '{id}' not found.")
        return JobStatusResponse(
            job_id=id,
            status=data.get("status", "pending"),
            actual_cost=data.get("actual_cost"),
            error_message=data.get("error_message"),
            error_code=data.get("error_code"),
            retryable=data.get("retryable", False),
            retry_after_seconds=data.get("retry_after_seconds"),
        )

    @app.api_route(
        "/storage/fake/{key:path}",
        methods=["GET", "HEAD"],
        summary="Retrieve stored asset from fake object storage",
    )
    def get_fake_storage_object(key: str):
        clean = key.lstrip("/")
        norm = posixpath.normpath(clean)
        if norm == ".." or norm.startswith("../") or any(part == ".." for part in norm.split("/")):
            raise HTTPException(
                status_code=400,
                detail=f"Invalid storage key: path traversal detected in '{key}'.",
            )

        storage = app.state.storage_adapter
        try:
            blob = storage.get(norm)
        except KeyError:
            raise HTTPException(
                status_code=404,
                detail=f"Storage object '{key}' not found.",
            )

        content_type = None
        if hasattr(storage, "get_content_type"):
            content_type = storage.get_content_type(norm)
        if not content_type:
            content_type, _ = mimetypes.guess_type(norm)
        if not content_type:
            content_type = "application/octet-stream"

        return Response(content=blob, media_type=content_type)

    return app


app = create_app()
